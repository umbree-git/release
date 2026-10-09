package main

import (
	"errors"

	"github.com/umbree-git/release/internal/manage/static"
)

var newStaticSource = r2StaticSource

func r2StaticSource(*options) (static.ManifestSource, error) { return nil, errors.New("not built") }
