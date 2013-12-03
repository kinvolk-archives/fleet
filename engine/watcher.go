package engine

import (
	"fmt"
	"log"
	"time"

	"github.com/coreos/coreinit/job"
	"github.com/coreos/coreinit/machine"
	"github.com/coreos/coreinit/registry"
)

const (
	DefaultJobWatchClaimTTL = "10s"
	DefaultRefreshInterval  = "60s"
)

type JobWatcher struct {
	registry        *registry.Registry
	scheduler       *Scheduler
	machine         *machine.Machine
	claimTTL        time.Duration
	refreshInterval time.Duration
	watches         map[string]job.JobWatch
	schedules       map[string]Schedule
	machines        map[string]machine.Machine
}

func NewJobWatcher(reg *registry.Registry, scheduler *Scheduler, m *machine.Machine) *JobWatcher {
	claimTTL, _ := time.ParseDuration(DefaultJobWatchClaimTTL)
	refreshInterval, _ := time.ParseDuration(DefaultRefreshInterval)

	jobs := make(map[string]job.JobWatch, 0)
	schedules := make(map[string]Schedule, 0)
	machines := make(map[string]machine.Machine, 0)

	return &JobWatcher{reg, scheduler, m, claimTTL, refreshInterval, jobs, schedules, machines}
}

func (self *JobWatcher) StartHeartbeatThread() {
	heartbeat := func() {
		for _, watch := range self.watches {
			self.registry.ClaimJobWatch(&watch, self.machine, self.claimTTL)
		}
	}

	loop := func() {
		for true {
			heartbeat()
			time.Sleep(self.claimTTL / 2)
		}
	}

	go loop()
}

func (self *JobWatcher) StartRefreshThread() {
	refresh := func() {
		machines := make(map[string]machine.Machine, 0)
		for _, m := range self.registry.GetActiveMachines() {
			machines[m.BootId] = m
		}
		self.machines = machines
	}

	loop := func() {
		for true {
			refresh()
			time.Sleep(self.refreshInterval)
		}
	}

	go loop()
}

func (self *JobWatcher) AddJobWatch(watch *job.JobWatch) bool {
	if !self.registry.ClaimJobWatch(watch, self.machine, self.claimTTL) {
		return false
	}

	self.watches[watch.Payload.Name] = *watch
	sched := NewSchedule()
	self.schedules[watch.Payload.Name] = sched

	if watch.Count == -1 {
		for _, m := range self.machines {
			name := fmt.Sprintf("%s.%s", m.BootId, watch.Payload.Name)
			j, _ := job.NewJob(name, nil, watch.Payload)
			log.Printf("EventJobWatchCreated(%s): adding to schedule job=%s machine=%s", watch.Payload.Name, name, m.BootId)
			sched.Add(*j, m)
		}
	} else {
		for i := 1; i <= watch.Count; i++ {
			m := pickRandomMachine(self.machines)
			name := fmt.Sprintf("%d.%s", i, watch.Payload.Name)
			j, _ := job.NewJob(name, nil, watch.Payload)
			log.Printf("EventJobWatchCreated(%s): adding to schedule job=%s machine=%s", watch.Payload.Name, name, m.BootId)
			sched.Add(*j, *m)
		}
	}

	if len(sched) > 0 {
		log.Printf("EventJobWatchCreated(%s): submitting schedule", watch.Payload.Name)
		self.submitSchedule(sched)
	} else {
		log.Printf("EventJobWatchCreated(%s): no schedule changes made", watch.Payload.Name)
	}

	return true
}

func (self *JobWatcher) RemoveJobWatch(watch *job.JobWatch) bool {
	if _, ok := self.watches[watch.Payload.Name]; !ok {
		return false
	}

	delete(self.watches, watch.Payload.Name)

	watchSchedule := self.schedules[watch.Payload.Name]
	delete(self.schedules, watch.Payload.Name)

	for job, mach := range watchSchedule {
		self.registry.RemoveMachineJob(&job, mach)
	}

	return true
}

func (self *JobWatcher) submitSchedule(schedule Schedule) {
	for j, m := range schedule {
		self.registry.ScheduleMachineJob(&j, m)
	}
}

func (self *JobWatcher) TrackMachine(m *machine.Machine) {
	self.machines[m.BootId] = *m

	partial := NewSchedule()
	for _, watch := range self.watches {
		if watch.Count == -1 {
			name := fmt.Sprintf("%s.%s", m.BootId, watch.Payload.Name)
			j, _ := job.NewJob(name, nil, watch.Payload)
			log.Printf("Adding to schedule job=%s machine=%s", name, m.BootId)
			partial.Add(*j, *m)

			sched := self.schedules[watch.Payload.Name]
			sched.Add(*j, *m)
		}
	}

	if len(partial) > 0 {
		log.Printf("Submitting schedule")
		self.submitSchedule(partial)
	} else {
		log.Printf("No schedule changes made")
	}
}

func (self *JobWatcher) DropMachine(m *machine.Machine) {
	if _, ok := self.machines[m.BootId]; ok {
		delete(self.machines, m.BootId)
	}
}
