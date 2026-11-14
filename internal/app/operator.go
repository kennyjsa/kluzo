package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kennyjsa/kluzo/internal/config"
)

// ErrNotOperator means who decides is not one of tracker.operators.
var ErrNotOperator = errors.New("who decides must be one of tracker.operators")

// operator is who makes a human decision through the CLI (plan.md): approve,
// proposal approve|reject, node retry|unblock|cancel and effect retry. It is
// by (--by), which must be one of tracker.operators, or the only operator
// when by is empty; it goes to the by of the event, never $USER.
func operator(p *config.Project, by string) (string, error) {
	operators := p.Config.Tracker.Operators
	if len(operators) == 0 {
		return "", fmt.Errorf("%w, and the list is empty: add the login of who decides to tracker.operators in kluzo.yaml", ErrNotOperator)
	}
	if by == "" {
		if len(operators) != 1 {
			return "", fmt.Errorf("%w (%s): name one with --by", ErrNotOperator, strings.Join(operators, ", "))
		}
		return operators[0], nil
	}
	for _, op := range operators {
		if strings.EqualFold(op, by) {
			return op, nil
		}
	}
	return "", fmt.Errorf("%w (%s): %q is not", ErrNotOperator, strings.Join(operators, ", "), by)
}
