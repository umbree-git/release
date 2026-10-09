package publish

import (
	"context"
	"errors"
	"io"
)

func Yank(ctx context.Context, d Deps, rowID int64, w io.Writer) error {
	st := newStream(w)
	err := errors.New("publish: yank not built")
	st.finish(err, rowID, "")
	return err
}
