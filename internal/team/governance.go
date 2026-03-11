package team

import (
	"context"
	"fmt"

	"github.com/junhoyeo/contrabass/internal/types"
)

type DecisionType string

const (
	DecisionClaim      DecisionType = "claim"
	DecisionTransition DecisionType = "transition"
	DecisionAssign     DecisionType = "assign"
)

type Decision struct {
	Type     DecisionType
	WorkerID string
	TaskID   string
	Phase    types.TeamPhase
	TeamName string

	WorkerActiveTasks         int
	ActiveWorkerCount         int
	CurrentPhaseTasksTerminal bool
}

type PolicyRule interface {
	Name() string
	Check(ctx context.Context, decision Decision) error
}

type GovernancePolicy struct {
	rules []PolicyRule
}

func NewGovernancePolicy(rules ...PolicyRule) *GovernancePolicy {
	return &GovernancePolicy{rules: rules}
}

func (p *GovernancePolicy) AddRule(rule PolicyRule) {
	if rule == nil {
		return
	}
	p.rules = append(p.rules, rule)
}

func (p *GovernancePolicy) Rules() []PolicyRule {
	rules := make([]PolicyRule, len(p.rules))
	copy(rules, p.rules)
	return rules
}

func (p *GovernancePolicy) Check(ctx context.Context, decision Decision) error {
	if ctx == nil {
		ctx = context.Background()
	}

	for _, rule := range p.rules {
		if err := ctx.Err(); err != nil {
			return err
		}
		if rule == nil {
			continue
		}
		if err := rule.Check(ctx, decision); err != nil {
			if violation, ok := err.(*PolicyViolation); ok {
				if violation.Rule == "" {
					violation.Rule = rule.Name()
				}
				if violation.Decision.Type == "" {
					violation.Decision = decision
				}
				return violation
			}
			return &PolicyViolation{
				Rule:     rule.Name(),
				Decision: decision,
				Err:      err,
			}
		}
	}

	return nil
}

type PolicyViolation struct {
	Rule     string
	Decision Decision
	Err      error
}

func (v *PolicyViolation) Error() string {
	if v == nil {
		return "governance policy violation"
	}
	if v.Err == nil {
		return fmt.Sprintf("governance policy violation: rule=%s decision=%s team=%s", v.Rule, v.Decision.Type, v.Decision.TeamName)
	}
	return fmt.Sprintf("governance policy violation: rule=%s decision=%s team=%s: %v", v.Rule, v.Decision.Type, v.Decision.TeamName, v.Err)
}

func (v *PolicyViolation) Unwrap() error {
	if v == nil {
		return nil
	}
	return v.Err
}

type MaxConcurrentTasksRule struct {
	MaxTasksPerWorker int
}

func (r MaxConcurrentTasksRule) Name() string {
	return "max_concurrent_tasks"
}

func (r MaxConcurrentTasksRule) Check(ctx context.Context, decision Decision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if decision.Type != DecisionClaim {
		return nil
	}
	if r.MaxTasksPerWorker <= 0 {
		return nil
	}
	if decision.WorkerActiveTasks >= r.MaxTasksPerWorker {
		return fmt.Errorf("worker %q has reached max concurrent tasks (%d)", decision.WorkerID, r.MaxTasksPerWorker)
	}
	return nil
}

type PhaseGateRule struct{}

func (r PhaseGateRule) Name() string {
	return "phase_gate"
}

func (r PhaseGateRule) Check(ctx context.Context, decision Decision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if decision.Type != DecisionTransition {
		return nil
	}
	if !decision.CurrentPhaseTasksTerminal {
		return fmt.Errorf("phase %q still has non-terminal tasks", decision.Phase)
	}
	return nil
}

type WorkerCapacityRule struct {
	MaxWorkers int
}

func (r WorkerCapacityRule) Name() string {
	return "worker_capacity"
}

func (r WorkerCapacityRule) Check(ctx context.Context, decision Decision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if decision.Type != DecisionClaim {
		return nil
	}
	if r.MaxWorkers <= 0 {
		return nil
	}
	if decision.ActiveWorkerCount > r.MaxWorkers {
		return fmt.Errorf("active worker count (%d) exceeds max workers (%d)", decision.ActiveWorkerCount, r.MaxWorkers)
	}
	return nil
}
