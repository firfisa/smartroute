package mihomolab

import (
	"context"

	"github.com/firfisa/smartroute/internal/testlab"
)

type tlsTarget = testlab.TLSTarget

func startTLSTarget(parent context.Context) (*tlsTarget, error) {
	return testlab.StartTLSTarget(parent)
}

func syntheticClientHelloRecords() []byte {
	return testlab.SyntheticClientHelloRecords()
}

func syntheticServerHelloRecord() []byte {
	return testlab.SyntheticServerHelloRecord()
}
