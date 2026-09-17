package app

import "context"

// Names 是用例消费的私有出站端口。
type Names interface {
	Find(context.Context, string) (string, error)
}

type Reader struct{ names Names }

func NewReader(names Names) *Reader { return &Reader{names: names} }

func (r *Reader) Name(ctx context.Context, id string) (string, error) {
	return r.names.Find(ctx, id)
}
