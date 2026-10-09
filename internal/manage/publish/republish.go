package publish

import (
	"context"
	"errors"
)

var ErrNoStatic = errors.New("publish: no static publisher is configured")

type StaticPublisher interface {
	Publish(ctx context.Context, component string) (string, error)
}

func RepublishStatic(ctx context.Context, d Deps, component, channel, actor string) (string, error) {
	return "", ErrNoStatic
}
