package main

import (
	"errors"

	"github.com/umbree-git/release/internal/manage/backend"
)

var newPublicStore = r2PublicStore

func r2PublicStore(o *options) (backend.Public, error) { return nil, errors.New("not built") }
