package state

import "context"

type Transaction struct {
	SchemaVersion string
	ID            string
	Command       string
	Status        string
}

type Store interface {
	Load(context.Context, string) (Transaction, error)
	Save(context.Context, Transaction) error
	Archive(context.Context, string) error
}
