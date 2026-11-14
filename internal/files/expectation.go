package files

import (
	"errors"
	"time"
)

var ErrPrecondition = errors.New("publication expectation failed")
var ErrConditionSyntax = errors.New("invalid condition")
var ErrConditionLimit = errors.New("condition limit exceeded")
var ErrNotModified = errors.New("representation not modified")

// TagExpectation and Expectation are transport-independent publication facts.
// Evaluate only against current state inside the operation's transaction.
type EntityTag struct {
	Value string
	Weak  bool
}
type TagExpectation struct {
	Present, Any bool
	Tags         []EntityTag
}
type Expectation struct {
	Match, NotMatch TagExpectation
	UnmodifiedSince time.Time
	Invalid         error
}

func (e TagExpectation) matches(node Node, exists, strong bool) bool {
	if e.Any {
		return exists
	}
	if !exists || node.ETag == "" {
		return false
	}
	for _, tag := range e.Tags {
		if (!strong || !tag.Weak) && tag.Value == node.ETag {
			return true
		}
	}
	return false
}
func (e Expectation) Check(node Node, exists bool) error {
	if e.Invalid != nil {
		return e.Invalid
	}
	if e.Match.Present {
		if !e.Match.matches(node, exists, true) {
			return ErrPrecondition
		}
	} else if exists && !e.UnmodifiedSince.IsZero() && node.Modified.After(e.UnmodifiedSince) {
		return ErrPrecondition
	}
	if e.NotMatch.Present && e.NotMatch.matches(node, exists, false) {
		return ErrNotModified
	}
	return nil
}
func (e Expectation) CheckWrite(node Node, exists bool) error {
	err := e.Check(node, exists)
	if errors.Is(err, ErrNotModified) {
		return ErrPrecondition
	}
	return err
}
