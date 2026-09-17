package customer

import (
	"github.com/wplbyx/modular/packages/core"

	"shop/modules/customer/contract"
	"shop/modules/customer/infrastructure/memory"
	"shop/modules/customer/internal/app"
)

type Dependencies struct {
	Names core.Provider[map[string]string]
}
type Module struct{ Reader contract.Reader }

func New(cfg Config, deps Dependencies) (*Module, error) {
	repository := memory.NewNames(deps.Names)
	return &Module{Reader: app.NewReader(repository)}, nil
}
