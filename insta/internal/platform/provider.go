package platform

import (
	"context"
	"errors"
	"fmt"
)

var ErrNotImplemented = errors.New("platform provider not implemented")

// Provider leaves resource ownership and effect approval in the Go CLI.
type Provider interface {
	Name() string
	Create(context.Context, []byte, func(Cluster) error) (Cluster, error)
	Get(context.Context, string) (Cluster, error)
	WaitReady(context.Context, string, func(Cluster) error) (Cluster, error)
	Delete(context.Context, string) error
	WaitDeleted(context.Context, string) error
}

// New exposes explicit placeholders, with no network activity, for future data
// platforms. Their deployment definitions are not consumed in this release.
func New(name, username, key string) (Provider, error) {
	switch name {
	case "instaclustr-opensearch":
		return NewOpenSearch(username, key)
	case "instaclustr-kafka", "instaclustr-cassandra", "instaclustr-clickhouse":
		return &Unimplemented{ProviderName: name}, nil
	default:
		return nil, errors.New("unknown platform provider")
	}
}

type Unimplemented struct{ ProviderName string }

func (p *Unimplemented) Name() string { return p.ProviderName }
func (p *Unimplemented) err() error   { return fmt.Errorf("%s: %w", p.ProviderName, ErrNotImplemented) }
func (p *Unimplemented) Create(context.Context, []byte, func(Cluster) error) (Cluster, error) {
	return Cluster{}, p.err()
}
func (p *Unimplemented) Get(context.Context, string) (Cluster, error) { return Cluster{}, p.err() }
func (p *Unimplemented) WaitReady(context.Context, string, func(Cluster) error) (Cluster, error) {
	return Cluster{}, p.err()
}
func (p *Unimplemented) Delete(context.Context, string) error      { return p.err() }
func (p *Unimplemented) WaitDeleted(context.Context, string) error { return p.err() }
