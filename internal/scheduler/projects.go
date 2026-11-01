package scheduler

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"time"

	"github.com/gurupras/agent-keepalive-manager/internal/model"
)

// Project rules keep every session of a project warm with one plan
// setting. On each scan, a reachable session of a ruled project that has no
// plan gets one, unless its last plan ended since the rule was set and no
// human prompt came after that end: the user cancelled it, or it ran out
// (a rolling plan after a long absence, an hours plan at its end), and
// starting it again at once would undo that. A plan the user cancels stays
// cancelled until the rule is set again.

// lastPlan is the newest plan a session had, as the rules need it.
type lastPlan struct {
	state   model.PlanState
	endedAt time.Time
}

var _ model.Projects = (*Service)(nil)

// ProjectRules returns the rules, by project.
func (s *Service) ProjectRules() []model.ProjectRule {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ruleList()
}

// ruleList requires s.mu.
func (s *Service) ruleList() []model.ProjectRule {
	out := make([]model.ProjectRule, 0, len(s.rules))
	for _, r := range s.rules {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Project < out[j].Project })
	return out
}

// SetProjectRule adds or changes the rule of r.Project. The plans the rule
// already started take the new setting from now, and sessions without a
// plan get one right away.
func (s *Service) SetProjectRule(ctx context.Context, r model.ProjectRule) (model.ProjectRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Project == "" || !filepath.IsAbs(r.Project) {
		return r, fmt.Errorf("%w: project must be an absolute path", model.ErrInvalid)
	}
	r.Project = filepath.Clean(r.Project)
	switch {
	case r.Indefinite && r.Hours != 0:
		return r, fmt.Errorf("%w: give either hours or indefinite, not both", model.ErrInvalid)
	case !r.Indefinite && (math.IsNaN(r.Hours) || r.Hours <= 0 || r.Hours > s.settings.MaxHours):
		return r, fmt.Errorf("%w: hours must be in (0, %g]", model.ErrInvalid, s.settings.MaxHours)
	}
	now := s.now()
	r.CreatedAt = now
	if err := s.cfg.Store.SaveProjectRule(context.WithoutCancel(ctx), r); err != nil {
		return r, fmt.Errorf("scheduler: save project rule: %w", err)
	}
	s.rules[r.Project] = r
	for _, key := range sortedKeys(s.plans) {
		if pr := s.plans[key]; pr.Project == r.Project {
			s.applyRule(ctx, now, pr, r)
		}
	}
	s.log.Info("scheduler: project rule set", "project", r.Project, "hours", r.Hours, "indefinite", r.Indefinite)
	s.publish("projects", s.ruleList())
	s.applyRules(ctx)
	s.rebuildViews(now)
	s.poke()
	return r, nil
}

// applyRule gives a plan the rule started the rule's new setting, from
// now. Requires s.mu.
func (s *Service) applyRule(ctx context.Context, now time.Time, pr *planRun, r model.ProjectRule) {
	win := s.window(pr.sess)
	pr.Indefinite = r.Indefinite
	if r.Indefinite {
		pr.RequestedHours = win
		pr.Until = latest(now, pr.sess.LastHuman).Add(hoursDur(win))
	} else {
		pr.RequestedHours = r.Hours
		pr.Until = now.Add(hoursDur(min(r.Hours, win)))
	}
	pr.Hours = pr.Until.Sub(pr.StartedAt).Hours()
	s.persist(ctx, pr)
	s.publish("plan", pr.Plan.Plan)
}

// RemoveProjectRule removes the rule of project and cancels the plans it
// started.
func (s *Service) RemoveProjectRule(ctx context.Context, project string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	project = filepath.Clean(project)
	if _, ok := s.rules[project]; !ok {
		return fmt.Errorf("%w: no rule for %s", model.ErrNotFound, project)
	}
	if err := s.cfg.Store.DeleteProjectRule(context.WithoutCancel(ctx), project); err != nil {
		return fmt.Errorf("scheduler: delete project rule: %w", err)
	}
	delete(s.rules, project)
	now := s.now()
	for _, key := range sortedKeys(s.plans) {
		if pr := s.plans[key]; pr.Project == project {
			s.endPlan(ctx, now, pr, model.PlanCancelled, model.EndCancelled, "the project is no longer kept warm")
		}
	}
	s.log.Info("scheduler: project rule removed", "project", project)
	s.publish("projects", s.ruleList())
	s.rebuildViews(now)
	return nil
}

// applyRules starts a plan for each session a rule should keep warm.
// Requires s.mu.
func (s *Service) applyRules(ctx context.Context) {
	if len(s.rules) == 0 {
		return
	}
	for _, key := range sortedKeys(s.sessions) {
		sess := s.sessions[key]
		r, ok := s.rules[sess.Project]
		if !ok || s.plans[key] != nil {
			continue
		}
		if _, err := s.reachable(key); err != nil {
			continue
		}
		if !s.ruleMayStart(ctx, r, sess) {
			continue
		}
		if _, err := s.start(ctx, key, r.Hours, r.Indefinite, r.Project); err != nil {
			s.log.Warn("scheduler: project rule could not start a plan", "project", r.Project, "key", key, "err", err)
		}
	}
}

// ruleMayStart says whether rule r may start a plan for sess (see the top
// of this file). Requires s.mu.
func (s *Service) ruleMayStart(ctx context.Context, r model.ProjectRule, sess model.Session) bool {
	last, ok := s.lastPlans[sess.Key]
	if !ok {
		p, found, err := s.cfg.Store.LastPlan(ctx, sess.Key)
		if err != nil {
			s.log.Warn("scheduler: reading the session's last plan failed", "key", sess.Key, "err", err)
			return false
		}
		if found {
			last = &lastPlan{state: p.State, endedAt: p.EndedAt}
		}
		s.lastPlans[sess.Key] = last
	}
	if last == nil || last.endedAt.Before(r.CreatedAt) {
		return true
	}
	if last.state == model.PlanCancelled {
		return false
	}
	return sess.LastHuman.After(last.endedAt)
}
