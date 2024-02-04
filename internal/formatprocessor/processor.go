// Package formatprocessor cleans and normalizes streams.
package formatprocessor

import (
	"time"

	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/pion/rtp"

	"github.com/bluenviron/mediamtx/internal/unit"
)

// avoid an int64 overflow and preserve resolution by splitting division into two parts:
// first add the integer part, then the decimal part.
func multiplyAndDivide(v, m, d time.Duration) time.Duration {
	secs := v / d
	dec := v % d
	return (secs*m + dec*m/d)
}

// Processor cleans and normalizes streams.
type Processor interface {
	// process a Unit.
	ProcessUnit(unit.Unit) error

	// process a RTP packet and convert it into a unit.
	ProcessRTPPacket(
		pkt *rtp.Packet,
		ntp time.Time,
		pts time.Duration,
		hasNonRTSPReaders bool,
	) (Unit, error)
}

// New allocates a Processor.
func New(
	udpMaxPayloadSize int,
	forma format.Format,
	generateRTPPackets bool,
) (Processor, error) {
	switch forma := forma.(type) {

	case *format.H264:
		return newH264(udpMaxPayloadSize, forma, generateRTPPackets)

	default:
		return newGeneric(udpMaxPayloadSize, forma, generateRTPPackets)
	}
}
