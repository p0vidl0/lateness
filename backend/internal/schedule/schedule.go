package schedule

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/p0vidl0/lateness/internal/domain"
)

const (
	minPeriod = time.Second
	maxPeriod = 366 * 24 * time.Hour
	maxSlack  = 30 * 24 * time.Hour
)

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// Validate checks an interval or a five-field cron expression. Cron uses the schedule timezone.
func Validate(spec domain.Schedule) error {
	if spec.Slack < 0 || spec.Slack > maxSlack {
		return fmt.Errorf("%w: slack", domain.ErrInvalid)
	}
	switch spec.Kind {
	case domain.ScheduleInterval:
		if spec.Period < minPeriod || spec.Period > maxPeriod {
			return fmt.Errorf("%w: period", domain.ErrInvalid)
		}
		return nil
	case domain.ScheduleCron:
		fields := strings.Fields(spec.Cron)
		if len(fields) != 5 {
			return fmt.Errorf("%w: cron must be five fields", domain.ErrInvalid)
		}
		if _, err := parser.Parse(spec.Cron); err != nil {
			return fmt.Errorf("%w: %s", domain.ErrInvalid, err.Error())
		}
		if _, err := cronLocation(spec.Timezone); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("%w: schedule kind", domain.ErrInvalid)
	}
}

// Next is the instant the schedule expects the next run: anchor plus period,
// or the next cron instant strictly after anchor.
func Next(spec domain.Schedule, anchor time.Time) (time.Time, error) {
	if err := Validate(spec); err != nil {
		return time.Time{}, err
	}
	anchor = anchor.UTC()
	if spec.Kind == domain.ScheduleInterval {
		return anchor.Add(spec.Period), nil
	}
	loc, err := cronLocation(spec.Timezone)
	if err != nil {
		return time.Time{}, err
	}
	sched, err := parser.Parse(spec.Cron)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %s", domain.ErrInvalid, err.Error())
	}
	return sched.Next(anchor.In(loc)).UTC(), nil
}

// Deadline is the instant the window closes: Next plus slack.
func Deadline(spec domain.Schedule, anchor time.Time) (time.Time, error) {
	next, err := Next(spec, anchor)
	if err != nil {
		return time.Time{}, err
	}
	return next.Add(spec.Slack), nil
}

// Closed reports whether now is at or past the deadline.
func Closed(spec domain.Schedule, anchor, now time.Time) (bool, error) {
	deadline, err := Deadline(spec, anchor)
	if err != nil {
		return false, err
	}
	return !now.UTC().Before(deadline), nil
}

func cronLocation(tz string) (*time.Location, error) {
	if tz == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("%w: timezone", domain.ErrInvalid)
	}
	return loc, nil
}
