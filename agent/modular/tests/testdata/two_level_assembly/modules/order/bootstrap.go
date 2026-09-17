package order

import (
	customercontract "shop/modules/customer/contract"
	"shop/modules/order/contract"
	"shop/modules/order/internal/app"
)

type Dependencies struct{ Customers customercontract.Reader }
type Module struct{ Summarizer contract.Summarizer }

func New(cfg Config, deps Dependencies) (*Module, error) {
	return &Module{Summarizer: app.NewSummarizer(deps.Customers)}, nil
}
