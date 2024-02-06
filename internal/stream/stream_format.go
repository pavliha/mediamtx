package stream

import (
	"sync/atomic"
	"time"

	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/pion/rtp"
	"github.com/sirupsen/logrus"

	"github.com/bluenviron/mediamtx/internal/asyncwriter"
	"github.com/bluenviron/mediamtx/internal/formatprocessor"
	"github.com/bluenviron/mediamtx/internal/unit"
)

func unitSize(u unit.Unit) uint64 {
	n := uint64(0)
	for _, pkt := range u.GetRTPPackets() {
		n += uint64(pkt.MarshalSize())
	}
	return n
}

type streamFormat struct {
	proc    *formatprocessor.FormatProcessorH264
	readers map[*asyncwriter.Writer]readerFunc
}

func newStreamFormat(forma format.Format) (*streamFormat, error) {
	proc, err := formatprocessor.NewH264(forma.(*format.H264))
	if err != nil {
		return nil, err
	}

	sf := &streamFormat{
		proc:    proc,
		readers: make(map[*asyncwriter.Writer]readerFunc),
	}

	return sf, nil
}

func (sf *streamFormat) addReader(r *asyncwriter.Writer, cb readerFunc) {
	sf.readers[r] = cb
}

func (sf *streamFormat) removeReader(r *asyncwriter.Writer) {
	delete(sf.readers, r)
}

func (sf *streamFormat) writeUnit(s *Stream, medi *description.Media, u unit.Unit) {
	err := sf.proc.ProcessUnit(u)
	if err != nil {
		logrus.Warn("[stream] write unit ", err)
		return
	}

	sf.writeUnitInner(s, medi, u)
}

func (sf *streamFormat) writeRTPPacket(
	s *Stream,
	medi *description.Media,
	pkt *rtp.Packet,
	ntp time.Time,
	pts time.Duration,
) {
	hasNonRTSPReaders := len(sf.readers) > 0

	u, err := sf.proc.ProcessRTPPacket(pkt, ntp, pts, hasNonRTSPReaders)
	if err != nil {
		logrus.Warn("[stream] process rtp packet ", err)
		return
	}

	sf.writeUnitInner(s, medi, u)
}

func (sf *streamFormat) writeUnitInner(s *Stream, medi *description.Media, u unit.Unit) {
	size := unitSize(u)

	atomic.AddUint64(s.bytesReceived, size)

	if s.rtspStream != nil {
		for _, pkt := range u.GetRTPPackets() {
			s.rtspStream.WritePacketRTPWithNTP(medi, pkt, u.GetNTP()) //nolint:errcheck
		}
	}

	for writer, cb := range sf.readers {
		ccb := cb
		writer.Push(func() error {
			atomic.AddUint64(s.bytesSent, size)
			return ccb(u)
		})
	}
}
