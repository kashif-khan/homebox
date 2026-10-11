package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/sysadminsmedia/homebox/backend/internal/core/alerts"
	"github.com/sysadminsmedia/homebox/backend/internal/core/regional"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
)

// AlertSink is told about alerts as they come due, in addition to the
// notifiers. Outbound webhooks hang off this. It receives every due alert,
// whether or not a notifier already had it, so it must be idempotent: a tick
// can see the same alert again until the catch-up window passes.
type AlertSink interface {
	Alerts(ctx context.Context, g repo.Group, now time.Time, due []alerts.Alert)
}

// SetAlertSink registers a sink for due alerts.
func (svc *BackgroundService) SetAlertSink(s AlertSink) { svc.onAlerts = s }

// SetSender replaces how messages reach notifiers. Tests use it.
func (svc *BackgroundService) SetSender(f func(n repo.NotifierOut, message string) error) {
	svc.send = f
}

func (svc *BackgroundService) sendToNotifier(n repo.NotifierOut, message string) error {
	if svc.send != nil {
		return svc.send(n, message)
	}
	if err := validate.ValidateNotifierURL(n.URL, svc.notifierConfig); err != nil {
		return fmt.Errorf("notifier %s failed validation: %w", n.Name, err)
	}
	return validate.SendNotifierMessage(n.URL, message, svc.notifierConfig)
}

// AlertTick sends whatever alerts have come due. It is cheap to call often: a
// collection is only looked at once its local send hour has arrived, and an
// alert that was already delivered is never sent again. Calling it again after
// an outage therefore catches up on what was missed, within alerts.CatchUpDays.
func (svc *BackgroundService) AlertTick(ctx context.Context, now time.Time) error {
	groups, err := svc.repos.Groups.GetAllGroups(ctx, uuid.Nil)
	if err != nil {
		return err
	}
	var errs []error
	for _, g := range groups {
		if err := svc.alertsForGroup(ctx, g, now); err != nil {
			log.Err(err).Str("group_id", g.ID.String()).Msg("alerts failed for collection")
			errs = append(errs, fmt.Errorf("collection %s: %w", g.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (svc *BackgroundService) alertsForGroup(ctx context.Context, g repo.Group, now time.Time) error {
	loc := regional.Location(g.Timezone)
	local := now.In(loc)
	hour := min(max(g.NotifyHour, 0), 23)
	if local.Hour() < hour {
		return nil // today's alerts are not due to go out yet
	}
	today := alerts.Day(local)

	if err := svc.repos.NotificationRules.SeedDefaults(ctx, g.ID); err != nil {
		log.Warn().Err(err).Str("group_id", g.ID.String()).Msg("could not create default alert rules")
	}
	rules, err := svc.repos.NotificationRules.Active(ctx, g.ID)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		return nil
	}
	active, err := svc.repos.Notifiers.GetActiveByGroup(ctx, g.ID)
	if err != nil {
		return err
	}

	// One digest per notifier across all rules, so a person gets one message a
	// day rather than one per rule.
	perNotifier := map[uuid.UUID][]alerts.Alert{}
	var sinkAlerts []alerts.Alert

	for _, rule := range rules {
		from, to := alerts.AnchorRange(today, rule.OffsetsDays)
		subjects, err := svc.repos.NotificationRules.Subjects(ctx, g.ID, rule, from, to)
		if err != nil {
			return fmt.Errorf("rule %q: %w", rule.Name, err)
		}
		if len(subjects) == 0 {
			continue
		}
		ar := alerts.Rule{ID: rule.ID, Name: rule.Name, Trigger: rule.Trigger, Offsets: rule.OffsetsDays}

		if svc.onAlerts != nil {
			sinkAlerts = append(sinkAlerts, alerts.Evaluate(ar, subjects, today, hour, loc, nil)...)
		}

		targets := targetNotifiers(active, rule.NotifierIDs)
		if len(targets) == 0 {
			continue
		}
		ids := make([]uuid.UUID, len(subjects))
		for i, s := range subjects {
			ids[i] = s.ID
		}
		done, err := svc.repos.NotificationRules.SentSet(ctx, rule.ID, ids)
		if err != nil {
			return fmt.Errorf("rule %q: %w", rule.Name, err)
		}
		for _, n := range targets {
			due := alerts.Evaluate(ar, subjects, today, hour, loc, func(s alerts.Subject, off int) bool {
				_, ok := done[repo.SentKey{NotifierID: n.ID, SubjectID: s.ID, Offset: off, Anchor: s.Anchor}]
				return ok
			})
			perNotifier[n.ID] = append(perNotifier[n.ID], due...)
		}
	}

	if svc.onAlerts != nil && len(sinkAlerts) > 0 {
		svc.onAlerts.Alerts(ctx, g, now, sinkAlerts)
	}

	var sendErrs []error
	for _, n := range active {
		due := perNotifier[n.ID]
		if len(due) == 0 {
			continue
		}
		msg := alerts.Digest(g.Name, today, append([]alerts.Alert(nil), due...))
		if err := svc.sendToNotifier(n, msg); err != nil {
			// Nothing is recorded, so the next tick tries again.
			log.Err(err).Str("notifier_id", n.ID.String()).Str("notifier_name", n.Name).Msg("alert delivery failed")
			sendErrs = append(sendErrs, err)
			continue
		}
		byRule := map[uuid.UUID][]repo.SentEntry{}
		for _, a := range due {
			byRule[a.Rule.ID] = append(byRule[a.Rule.ID], repo.SentEntry{
				SentKey: repo.SentKey{NotifierID: n.ID, SubjectID: a.Subject.ID, Offset: a.Offset, Anchor: a.Subject.Anchor},
				DueOn:   a.DueOn,
				SentAt:  now,
			})
		}
		for ruleID, entries := range byRule {
			if err := svc.repos.NotificationRules.RecordSent(ctx, ruleID, entries); err != nil {
				// The message went out; failing to note it risks one repeat, which
				// is better than losing it.
				log.Err(err).Str("rule_id", ruleID.String()).Msg("could not record sent alerts")
			}
		}
	}
	return errors.Join(sendErrs...)
}

// targetNotifiers picks the notifiers a rule sends to: the ones it names, or
// every active one when it names none.
func targetNotifiers(active []repo.NotifierOut, named []uuid.UUID) []repo.NotifierOut {
	if len(named) == 0 {
		return active
	}
	want := make(map[uuid.UUID]struct{}, len(named))
	for _, id := range named {
		want[id] = struct{}{}
	}
	var out []repo.NotifierOut
	for _, n := range active {
		if _, ok := want[n.ID]; ok {
			out = append(out, n)
		}
	}
	return out
}

// PurgeAlertLog forgets old send records.
func (svc *BackgroundService) PurgeAlertLog(ctx context.Context, now time.Time) (int, error) {
	return svc.repos.NotificationRules.PurgeLogs(ctx, now.AddDate(0, 0, -(alerts.CatchUpDays+60)))
}
