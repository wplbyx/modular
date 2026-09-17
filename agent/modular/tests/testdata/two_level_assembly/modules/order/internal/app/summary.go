package app

import (
	"context"

	customercontract "shop/modules/customer/contract"
)

type Summarizer struct{ customers customercontract.Reader }

func NewSummarizer(customers customercontract.Reader) *Summarizer {
	return &Summarizer{customers: customers}
}

func (s *Summarizer) Summary(ctx context.Context, id string) (string, error) {
	name, err := s.customers.Name(ctx, id)
	if err != nil {
		return "", err
	}
	return "Order for " + name, nil
}
