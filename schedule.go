package main

import (
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
)

func priorityRule(typeStr string, critical bool) int {
	if critical {
		return 0
	}
	if typeStr == "manual" {
		return 1
	}
	return 2
}

type Task struct {
	ID string
	StartDatetime time.Time
	Duration time.Duration
	Deadline time.Time
	Zones []string
	Type string // auto or manual
	Critical bool // only for manual type
	Priority int // 0 for critical, 1, for manual noncritical, 2 for auto
	Status string // wait, progress, complete or cancel
}

var tasks = make(map[string]*Task)

type ScheduleZone []string
var schedule = make(map[string]ScheduleZone)

func cancelTask(taskId string) {  // tasks are cancelled in all zones specified for the task
	log.Debug("Cancelling task ", taskId)
	if tasks[taskId].Status != "cancel" {
		for _, zone := range tasks[taskId].Zones {
			idx := 0
			for i := range schedule[zone] {
				if schedule[zone][i] == taskId {
					idx = i
				}
			}
			schedule[zone] = append(schedule[zone][:idx], schedule[zone][idx+1:]...)
		}
		tasks[taskId].Status = "cancel"
	}
}

func insertTask(taskId string, idx int, zone string) {
	log.Debug("Inserting task ", taskId, " into schedule...")
	if len(schedule[zone]) == idx { // nil or empty slice or after last element
        schedule[zone] = append(schedule[zone], taskId)
		return
    }
    schedule[zone] = append(schedule[zone][:idx+1], schedule[zone][idx:]...) // index < len(schedue)
    schedule[zone][idx] = taskId
}

type Order struct {
	zone			string
	cancelTaskIds 	[]string
	addIdx			int
	taskID			string
}

func executeOrder(order Order) {
	for _, taskId := range order.cancelTaskIds {
		cancelTask(taskId)
	}
	insertTask(order.taskID, order.addIdx, order.zone)
}

func overlap(start1 time.Time, end1 time.Time, start2 time.Time, end2 time.Time) bool {
	if start1.After(start2) {
		return overlap(start2, end2, start1, end1)
	}
	if !end1.Before(start2) {
		return true
	}
	return false
}

func availableTimeZone(task *Task) error {
	// check that task is scheduled in available zone in available time
	startTime := task.StartDatetime
	endTime := task.StartDatetime.Add(task.Duration)
	// hack: cut off the date part and reconvert to time.Time to compare with whiteListed timespans
	startTimeConverted, _ := time.Parse("15:04", startTime.Format("15:04"))
	endTimeConverted, _ := time.Parse("15:04", endTime.Format("15:04"))
	for _, zone := range task.Zones {
		zoneExists := false
		for _, blackListZone := range config.BlackList {
			if zone == blackListZone {
				zoneExists = true
				if !task.Critical {
					return fmt.Errorf("one of zones is in blackList and task is not critical: %s", zone)
				}
			}
		}
		availableZones := len(config.WhiteList)
		for whiteListZone, zoneTimeSpans := range config.WhiteList {
			if zone == whiteListZone {
				availableZones -= 1
				zoneExists = true
				valid := false
				for _, timeSpan := range zoneTimeSpans {
					log.Debug(fmt.Sprintf("Comparing timespans... startTime: %v, endTime: %v, timeSpan: %v", startTimeConverted, endTimeConverted, timeSpan))
					if startTimeConverted.After(timeSpan.Start) && endTimeConverted.Before(timeSpan.End) {
						valid = true
					}
				}
				if !valid {
					return fmt.Errorf("does not match any timespan in zone: %s", zone)
				}
			} else {
			 	// ensure that there are at least config.availableZones available
				zoneSchedule, ok := schedule[whiteListZone]
				if ok {
					for i := range zoneSchedule {
						schedTask := tasks[zoneSchedule[i]]
						if overlap(schedTask.StartDatetime, schedTask.StartDatetime.Add(schedTask.Duration), startTime, endTime) {
							availableZones -= 1
							break
						}
					}
				}
			}
		}
		if availableZones < config.AvailableZones {
			return fmt.Errorf("can't schedule task; %d zones should be available at all times", config.AvailableZones)
		}
		if !zoneExists {
			return fmt.Errorf("no such zone exists in config")
		}
	}
	return nil
}

func availablePrioritizedTimespan(task *Task, zone string) (Order, error) {
	order := Order{
		zone: zone, 
		taskID: task.ID,
	}
	startIdx := 0
	zoneSchedule, ok := schedule[zone]
	if ok {
		overlaps := []int{}
		for i := range zoneSchedule {
			schedTask := tasks[zoneSchedule[i]]
			schedTaskStart := schedTask.StartDatetime
			schedTaskEnd := schedTaskStart.Add(tasks[zoneSchedule[i]].Duration).Add(config.Pauses[zone])  // added zone-specific pauses
			if overlap(schedTaskStart, schedTaskEnd, task.StartDatetime, task.StartDatetime.Add(task.Duration)) {
				overlaps = append(overlaps, i)
			}
			if task.StartDatetime.After(schedTaskEnd) {
				startIdx = i + 1
			}
		}
		// no overlaps
		if len(overlaps) == 0 {
			order.cancelTaskIds = []string{}
			order.addIdx = startIdx
			return order, nil
		}
		// there are overlaps; check priorities and status (if "cancel", then the task is set for cancellation/extension/move)
		for i := range overlaps {
			schedTask := tasks[zoneSchedule[i]]
			if schedTask.Priority <= task.Priority && schedTask.Status != "cancel" {
				return order, fmt.Errorf("can't schedule task; overlap in zone %s with task with priority %d %s (%s, critical: %v), %v-%v", zone, schedTask.Priority, schedTask.ID, schedTask.Type, schedTask.Critical, schedTask.StartDatetime, schedTask.StartDatetime.Add(schedTask.Duration))
			}
		}
		// no priority overlaps; cancel less prioritized overlapping tasks
		for i := range overlaps {
			order.cancelTaskIds = append(order.cancelTaskIds, tasks[zoneSchedule[i]].ID)
		}
	}
	order.addIdx = startIdx
	return order, nil
}

func availableTimespan(task *Task) error {
	orders := []Order{}
	for _, zone := range task.Zones {
		order, err := availablePrioritizedTimespan(task, zone)
		if err != nil {
			return err
		}
		orders = append(orders, order)
	}
	fmt.Println(orders)
	for _, order := range orders {
		log.Debug("Executing order in zone ", order.zone)
		executeOrder(order)
	}

	return nil
}

func scheduleTask(task *Task) error {
	status := task.Status
	task.Status = "cancel"

	err := availableTimeZone(task)
	if err != nil {
		return err
	}
	
	err = availableTimespan(task)
	if err != nil {
		return err
	}
	task.Status = status

	return nil
}

func reschedule() error {
	// TODO: rescheduler "cancels" all tasks and recreates schedule
	return nil
}