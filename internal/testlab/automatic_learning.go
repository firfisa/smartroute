package testlab

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/firfisa/smartroute/internal/learning"
	"github.com/firfisa/smartroute/internal/model"
	"github.com/firfisa/smartroute/internal/privacy"
	"github.com/firfisa/smartroute/internal/sidecar"
	"github.com/firfisa/smartroute/internal/socks5"
	"github.com/firfisa/smartroute/internal/store"
	"github.com/firfisa/smartroute/internal/transport"
)

const automaticTestProfile = "isolated-auto-learning"

type automaticLabLearner struct {
	index *store.DurablePolicyIndex
}

func newAutomaticLabLearner() (*automaticLabLearner, error) {
	index, err := store.NewDurablePolicyIndex(bytes.Repeat([]byte{0x42}, 32), nil, 16)
	if err != nil {
		return nil, err
	}
	return &automaticLabLearner{index: index}, nil
}

func (l *automaticLabLearner) FixedPath(target model.Target) model.Path {
	return l.index.PreferredPath(target)
}

func (l *automaticLabLearner) PreferredPath(model.Target) model.Path { return "" }

func (l *automaticLabLearner) Observe(target model.Target, winner model.Observation, other *model.Observation) (learning.Update, error) {
	direction, reason, err := learning.ClassifyStrongPair(winner, other)
	if err != nil {
		return learning.Update{}, err
	}
	change, err := l.index.Remember(target, winner.Path, time.Now().UTC())
	if err != nil {
		return learning.Update{}, err
	}
	state := model.StateDirectPreferred
	if winner.Path == model.PathProxy {
		state = model.StateProxyPreferred
	}
	if direction != "" {
		reason = learning.ReasonDirectEvidence
		if direction == model.PathProxy {
			reason = learning.ReasonProxyEvidence
		}
	}
	return learning.Update{
		Applied: change.Applied,
		Policy: learning.Policy{
			State: state, PreferredPath: winner.Path, UpdatedAt: change.Policy.UpdatedAt,
		},
		ReasonCode: reason,
	}, nil
}

type automaticStep struct {
	name            string
	expectedPath    model.Path
	expectedReason  string
	expectedLearned model.Path
	directAttempts  int
	proxyAttempts   int
	before          func(*fakeGateway, *fakeGateway)
}

func runAutomaticLearningScenarios(parent context.Context) ([]ScenarioResult, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	targetServer, err := StartTLSTarget(ctx)
	if err != nil {
		return nil, err
	}
	defer targetServer.Close()
	direct, err := startGateway(ctx, targetServer.Address(), gatewayBehavior{})
	if err != nil {
		return nil, err
	}
	defer direct.Close()
	proxy, err := startGateway(ctx, targetServer.Address(), gatewayBehavior{})
	if err != nil {
		return nil, err
	}
	defer proxy.Close()
	learner, err := newAutomaticLabLearner()
	if err != nil {
		return nil, err
	}
	privacyPolicy, err := privacy.New(privacy.ModeExplicitOptIn, nil)
	if err != nil {
		return nil, err
	}
	listener, err := listenLoopback()
	if err != nil {
		return nil, fmt.Errorf("listen automatic-learning sidecar: %w", err)
	}
	defer listener.Close()
	events := make(chan sidecar.DecisionEvent, 4)
	server := sidecar.Server{
		NetworkProfileID:     automaticTestProfile,
		DeclaredBaselinePath: model.PathProxy,
		HandshakeTimeout:     time.Second,
		MaxClientHelloBytes:  64 * 1024,
		DirectProbePolicy:    privacyPolicy,
		Learning:             learner,
		TLSRacer: &transport.TLSRacer{
			Direct: transport.SOCKS5Dialer{Path: model.PathDirect, Endpoint: direct.Address(), ReadinessStage: model.StageOutbound},
			Proxy:  transport.SOCKS5Dialer{Path: model.PathProxy, Endpoint: proxy.Address(), ReadinessStage: model.StageOutbound},
			Gate:   transport.TLSServerHelloGate{}, HeadStart: 100 * time.Millisecond, Timeout: time.Second,
		},
		OnDecision: func(event sidecar.DecisionEvent) { events <- event },
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(ctx, listener) }()

	steps := []automaticStep{
		{
			name: "auto_first_ready_remembered", expectedPath: model.PathDirect,
			expectedReason: transport.ReasonDirectCandidateBeforeHeadStart, expectedLearned: model.PathDirect,
			directAttempts: 1, proxyAttempts: 0,
		},
		{
			name: "auto_reuses_direct_without_proxy", expectedPath: model.PathDirect,
			expectedReason: transport.ReasonDurablePolicySelected, expectedLearned: model.PathDirect,
			directAttempts: 1, proxyAttempts: 0,
		},
		{
			name: "auto_fallback_overwrites_proxy", expectedPath: model.PathProxy,
			expectedReason: transport.ReasonDurablePolicyFallback, expectedLearned: model.PathProxy,
			directAttempts: 1, proxyAttempts: 1,
			before: func(direct, proxy *fakeGateway) {
				direct.setBehavior(gatewayBehavior{fail: true})
				proxy.setBehavior(gatewayBehavior{})
			},
		},
		{
			name: "auto_reuses_proxy_without_direct", expectedPath: model.PathProxy,
			expectedReason: transport.ReasonDurablePolicySelected, expectedLearned: model.PathProxy,
			directAttempts: 0, proxyAttempts: 1,
		},
	}

	requestTarget := model.Target{
		NetworkProfileID: automaticTestProfile, Hostname: testHostname,
		Port: targetServer.Port(), Transport: model.TransportTCP,
	}
	results := make([]ScenarioResult, 0, len(steps))
	for _, step := range steps {
		if step.before != nil {
			step.before(direct, proxy)
		}
		directBefore, _ := direct.Snapshot()
		proxyBefore, _ := proxy.Snapshot()
		started := time.Now()
		event, readinessVerified, connectionErr := exerciseTLSConnection(ctx, listener.Addr().String(), targetServer.Port(), events)
		directAfter, directHost := direct.Snapshot()
		proxyAfter, proxyHost := proxy.Snapshot()
		result := ScenarioResult{
			Name: step.name, ExpectedPath: step.expectedPath, SelectedPath: event.SelectedPath,
			ReasonCode: event.ReasonCode, DirectAttempts: directAfter - directBefore,
			ProxyAttempts: proxyAfter - proxyBefore, ReadinessVerified: readinessVerified,
			LearnedPath: learner.FixedPath(requestTarget), ElapsedMS: time.Since(started).Milliseconds(),
		}
		result.DomainPreserved = (result.DirectAttempts == 0 || directHost == testHostname) &&
			(result.ProxyAttempts == 0 || proxyHost == testHostname)
		result.Passed = connectionErr == nil && result.SelectedPath == step.expectedPath &&
			result.ReasonCode == step.expectedReason && result.LearnedPath == step.expectedLearned &&
			result.DirectAttempts == step.directAttempts && result.ProxyAttempts == step.proxyAttempts &&
			result.ReadinessVerified && result.DomainPreserved
		if connectionErr != nil {
			result.FailureReason = connectionErr.Error()
		} else if !result.Passed {
			result.FailureReason = fmt.Sprintf("automatic learning invariant mismatch: selected=%s reason=%s learned=%s attempts=%d/%d readiness=%t domain=%t",
				result.SelectedPath, result.ReasonCode, result.LearnedPath, result.DirectAttempts, result.ProxyAttempts,
				result.ReadinessVerified, result.DomainPreserved)
		}
		results = append(results, result)
	}
	return results, nil
}

func exerciseTLSConnection(ctx context.Context, sidecarAddress string, port uint16, events <-chan sidecar.DecisionEvent) (sidecar.DecisionEvent, bool, error) {
	client, err := socks5.DialContext(ctx, sidecarAddress, socks5.Request{Host: testHostname, Port: port})
	if err != nil {
		return sidecar.DecisionEvent{}, false, err
	}
	defer client.Close()
	if _, err := client.Write(SyntheticClientHelloRecords()); err != nil {
		return sidecar.DecisionEvent{}, false, fmt.Errorf("write synthetic ClientHello: %w", err)
	}
	expected := SyntheticServerHelloRecord()
	received := make([]byte, len(expected))
	if _, err := io.ReadFull(client, received); err != nil {
		return sidecar.DecisionEvent{}, false, fmt.Errorf("read synthetic ServerHello: %w", err)
	}
	var event sidecar.DecisionEvent
	select {
	case event = <-events:
	case <-ctx.Done():
		return sidecar.DecisionEvent{}, false, ctx.Err()
	}
	if !event.Committed {
		return event, false, errors.New("automatic learning decision did not commit")
	}
	return event, bytes.Equal(received, expected), nil
}
