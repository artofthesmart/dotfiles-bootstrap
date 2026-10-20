package testkit

import (
	"errors"
	"sync"
)

type FailurePhase string

const (
	BeforeMutation FailurePhase = "before"
	AfterMutation  FailurePhase = "after"
)

var ErrInjectedFailure = errors.New("injected failure")

type FailureRecord struct {
	Operation  string
	Phase      FailurePhase
	Occurrence int
	Triggered  bool
}

type failureRule struct {
	operation  string
	phase      FailurePhase
	occurrence int
	err        error
}

type FailureInjector struct {
	mu      sync.Mutex
	rules   []failureRule
	hits    map[string]int
	records []FailureRecord
}

func NewFailureInjector() *FailureInjector {
	return &FailureInjector{hits: make(map[string]int)}
}

func (f *FailureInjector) Inject(operation string, phase FailurePhase, occurrence int, err error) {
	if occurrence < 1 {
		occurrence = 1
	}
	if err == nil {
		err = ErrInjectedFailure
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = append(f.rules, failureRule{
		operation:  operation,
		phase:      phase,
		occurrence: occurrence,
		err:        err,
	})
}

func (f *FailureInjector) Fail(operation string, phase FailurePhase) error {
	if f == nil {
		return nil
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	key := operation + ":" + string(phase)
	f.hits[key]++
	record := FailureRecord{
		Operation:  operation,
		Phase:      phase,
		Occurrence: f.hits[key],
	}
	for _, rule := range f.rules {
		if rule.operation == operation && rule.phase == phase && rule.occurrence == record.Occurrence {
			record.Triggered = true
			f.records = append(f.records, record)
			return rule.err
		}
	}
	f.records = append(f.records, record)
	return nil
}

func (f *FailureInjector) Records() []FailureRecord {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FailureRecord(nil), f.records...)
}
