package stream

import (
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
)

type streamMedia struct {
	formats map[format.Format]*streamFormat
}

func newStreamMedia(
	medi *description.Media,
) (*streamMedia, error) {
	sm := &streamMedia{
		formats: make(map[format.Format]*streamFormat),
	}

	for _, forma := range medi.Formats {
		var err error
		sm.formats[forma], err = newStreamFormat(forma)
		if err != nil {
			return nil, err
		}
	}

	return sm, nil
}
