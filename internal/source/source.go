package source

import (
	"context"
	"rightsize/internal/model"
)

// Source is the only thing the rest of the program knows about where
// readings come from. Go interfaces are satisfied implicitly: any type with
// this method *is* a Source, with no "implements" keyword. That's why the
// interface lives with the consumer's needs, and why it's tiny.
type Source interface {
	Usage(ctx context.Context) ([]model.Usage, error)
}
