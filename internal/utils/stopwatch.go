package utils

import (
	"fmt"
	"time"
)

type Stopwatch struct {
	startTime time.Time
	activity  string
}

func NewStopwatch(activity string) *Stopwatch {
	return &Stopwatch{
		startTime: time.Now(),
		activity:  activity,
	}
}

func (sw *Stopwatch) Stop() {
	fmt.Printf("%s took %s\n", sw.activity, time.Now().Sub(sw.startTime))
}
