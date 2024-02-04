package defs

import (
	"context"

	"github.com/bluenviron/mediamtx/internal/conf"
)

// StaticSource is a static source.
type StaticSource interface {
	Run(StaticSourceRunParams) error
}

// StaticSourceParent is the parent of a static source.
type StaticSourceParent interface {
	SetReady(req PathSourceStaticSetReadyReq) PathSourceStaticSetReadyRes
	SetNotReady(req PathSourceStaticSetNotReadyReq)
}

// StaticSourceRunParams is the set of params passed to Run().
type StaticSourceRunParams struct {
	Context context.Context
	Conf    *conf.Path
}
