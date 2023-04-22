package main

import (
	"fmt"
	"time"
	"sort"
	"strings"

	log "github.com/sirupsen/logrus"
	"go.uber.org/multierr"
	"github.com/google/uuid"
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

func removeDuplicateTime(timeSlice []time.Time) []time.Time {
    allKeys := make(map[time.Time]bool)
    list := []time.Time{}
    for _, item := range timeSlice {
        if _, value := allKeys[item]; !value {
            allKeys[item] = true
            list = append(list, item)
        }
    }
    return list
}

type Task struct {
	ID 						string
	Name					string
	PreferredStartDatetime	time.Time
	StartDatetime 			time.Time
	Duration 				time.Duration
	Deadline 				time.Time
	Zones 					[]string
	Type 					string // auto or manual
	Critical 				bool // only for manual type
	Priority 				int // 0 for critical, 1, for manual noncritical, 2 for auto
	CompressionPerc 		int // from 0 to 100; for auto only
	Status 					string // wait, suggested, cancel, change (move + extend, enables rescheduling for <= prioritized) (progress and complete in production)
}

var tasks = make(map[string]*Task)

type ScheduleZone []string
var schedule = make(map[string]ScheduleZone)

func cancelTask(taskId string) {  // tasks are cancelled in all zones specified for the task
	log.Debug("Cancelling task ", taskId)
	_, ok := tasks[taskId]
	if ok {
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

func wipeTask(taskId string) {
	cancelTask(taskId)
	delete(tasks, taskId)
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

func splitTask(task Task) []string {  // splitting done for rescheduling + compression if available
	if len(task.Zones) == 1 {
		return []string{task.ID}
	}
	newTaskIds := []string{}
	for _, zone := range task.Zones {
		newTask := task
		newTask.ID = uuid.New().String()
		newTask.Zones = []string{zone}
		newTask.StartDatetime = newTask.PreferredStartDatetime
		newTask.Duration = time.Duration(int(task.Duration.Nanoseconds()) * (100 - task.CompressionPerc) / 100)
		tasks[newTask.ID] = &newTask
		newTaskIds = append(newTaskIds, newTask.ID)
	}
	return newTaskIds
}

type Order struct {
	zone			string
	reschedTaskIds 	[]string
	addIdx			int
	taskID			string
}

func executeOrder(order Order) {
	for _, taskId := range order.reschedTaskIds {
		cancelTask(taskId)
	}
	insertTask(order.taskID, order.addIdx, order.zone)
	for _, taskId := range order.reschedTaskIds {
		splitTaskIds := splitTask(*tasks[taskId])
		for _, newTaskId := range splitTaskIds {
			newTask := tasks[newTaskId]
			points := suggestTime(*newTask)
			if len(points) == 0 {
				log.Warn(fmt.Sprintf("Cancelled split task %s for zone %v from parent task %s", newTaskId, newTask.Zones, taskId))
				continue
			}
			newTask.StartDatetime = points[newTask.Zones[0]]
			err := scheduleTask(tasks[newTaskId], "wait")
			if err != nil {
				log.Warn(fmt.Sprintf("Cancelled split task %s for zone %v from parent task %s", newTaskId, newTask.Zones, taskId))
			}
		}
	}
}

func overlap(start1 time.Time, end1 time.Time, start2 time.Time, end2 time.Time) bool {
	if start1.After(start2) {
		return overlap(start2, end2, start1, end1)
	}
	if end1.After(start2) {
		return true
	}
	return false
}

func pointsOfInterestTime(addPoints []time.Time) []time.Time {
	// merge starttimes and endtimes from all zones
	pointsTime := []time.Time{}
	for zone := range config.WhiteList {
		zoneSchedule, ok := schedule[zone]
		if ok {
			for i := range zoneSchedule {
				pointsTime = append(pointsTime, tasks[zoneSchedule[i]].StartDatetime)
				pointsTime = append(pointsTime, tasks[zoneSchedule[i]].StartDatetime.Add(tasks[zoneSchedule[i]].Duration).Add(config.Pauses[zone]))
			}
		}
	}
	// and add addPoints
	pointsTime = append(pointsTime, addPoints...)
	pointsTime = removeDuplicateTime(pointsTime)

	// merge all timezone whitelist times
	configZonePoints := []time.Time{}
	for _, timeSpans := range config.WhiteList {
		for _, timeSpan := range timeSpans {
			configZonePoints = append(configZonePoints, timeSpan.Start)
			configZonePoints = append(configZonePoints, timeSpan.End)
		}
	}
	configZonePoints = removeDuplicateTime(configZonePoints)

	// sort to get the earliest and latest and add timezone work start times
	sort.Slice(pointsTime, func(i, j int) bool {
		return pointsTime[i].Before(pointsTime[j])
	})
	pointsTime = removeDuplicateTime(pointsTime)
	earliest := pointsTime[0]
	latest := pointsTime[len(pointsTime) - 1]
	for day := 0; day < int(latest.Sub(earliest).Hours()) / 24 + 1; day++ {
		earliest = earliest.Add(time.Hour * 24)
		for _, zonePoint := range configZonePoints {
			pointsTime = append(pointsTime, time.Date(earliest.Year(), earliest.Month(), earliest.Day(), zonePoint.Hour(), zonePoint.Minute(), zonePoint.Second(), zonePoint.Nanosecond(), zonePoint.Location()))
		}
	}

	// resulting sort
	sort.Slice(pointsTime, func(i, j int) bool {
		return pointsTime[i].Before(pointsTime[j])
	})
	return pointsTime
}

func suggestTime(task Task) map[string]time.Time {
	suggestions := make(map[string]time.Time)
	// create slice with points of interest (merge times from all zones, insert starts of available time zone times) and sort
	addPoints := []time.Time{task.StartDatetime}
	for _, zone := range task.Zones {
		addPoints = append(addPoints, task.StartDatetime.Add(task.Duration).Add(config.Pauses[zone]))
	}
	pointsTime := pointsOfInterestTime(addPoints)
	fmt.Println(pointsTime)

	// split tasks and create dummies for each
	dummyTasks := []string{}
	for _, zone := range task.Zones {
		dummyTask := task
		dummyTask.ID = uuid.New().String()
		dummyTask.Status = "suggested"
		dummyTask.Zones = []string{zone}
		for _, point := range pointsTime {
			if point.Before(task.StartDatetime) {
				continue
			}
			if point.Add(task.Duration).After(task.Deadline) {
			 	break
			}
			dummyTask.StartDatetime = point
			err := availableTimeZone(&dummyTask)
			if err != nil {
				fmt.Println(err)
				continue
			}
			dummyOrder, err := availablePrioritizedTimespan(&dummyTask, zone)
			if err != nil {
				fmt.Println(err)
				continue
			}
			dummyOrder.reschedTaskIds = []string{}
			fmt.Println(dummyOrder)
			executeOrder(dummyOrder)
			tasks[dummyTask.ID] = &dummyTask
			dummyTasks = append(dummyTasks, dummyTask.ID)
			suggestions[zone] = point
			break
		}
	}
	// delete all dummy tasks
	for _, dummy := range dummyTasks {
		wipeTask(dummy)
	}
	if len(suggestions) < len(task.Zones) {
		return nil
	}
	return suggestions
}

func suggestTimeString(task Task) string {
	points := suggestTime(task)
	if len(points) == 0 {
		return "Task REJECTED. No timespans are available."
	}
	suggestions := []string{}
	for zone, point := range points {
		suggestions = append(suggestions, fmt.Sprintf("  - %s: %s - %s", zone, point.Format("15:04 02/01/2006"), point.Add(task.Duration).Format("15:04 02/01/2006")))
	} 
	return "Please review suggested timespans:\n" + strings.Join(suggestions, "\n")
}

func countUnavailableZones(taskCount int, zone string, priority int, startTime time.Time, endTime time.Time) int {
	unavailableZones := taskCount
	splits := []time.Time{}
	overlapIdxs := make(map[string][]int)
	for whiteListZone, _ := range config.WhiteList {
		if zone != whiteListZone {
			zoneSchedule, ok := schedule[whiteListZone]
			if ok {
				for i := range zoneSchedule {
					schedTask := tasks[zoneSchedule[i]]
					schedStart := schedTask.StartDatetime
					schedEnd := schedTask.StartDatetime.Add(schedTask.Duration)
					if schedStart.After(startTime) && schedStart.Before(endTime) {
						splits = append(splits, schedStart)
					}
					if schedEnd.After(startTime) && schedEnd.Before(endTime) {
						splits = append(splits, schedEnd)
					}
					if overlap(schedStart, schedEnd, startTime, endTime) {
						overlapIdxs[whiteListZone] = append(overlapIdxs[whiteListZone], i)
					}
				}
			}
		}
	}
	splits = removeDuplicateTime(splits)
	unavailablePerSplit := make([]int, len(splits))
	for i, split := range splits {
		startSplitTime := startTime
		endSplitTime := split
		if i != 0 {
			startSplitTime = splits[i-1]
		}
		for zone, idxs := range overlapIdxs {
			for _, idx := range idxs {
				schedTask := tasks[schedule[zone][idx]]
				if overlap(startSplitTime, endSplitTime, schedTask.StartDatetime, schedTask.StartDatetime.Add(schedTask.Duration)) && schedTask.Priority <= priority && schedTask.Status != "cancel" {
					unavailablePerSplit[i] += 1
				}
			}
		}
	}
	maxUnavailable := 0
	for _, value := range unavailablePerSplit {
		if value > maxUnavailable {
			maxUnavailable = value
		}
	}
	unavailableZones += maxUnavailable
	return unavailableZones
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
		for whiteListZone, zoneTimeSpans := range config.WhiteList {
			if zone == whiteListZone {
				zoneExists = true
				valid := false
				for _, timeSpan := range zoneTimeSpans {
					log.Debug(fmt.Sprintf("Comparing timespans... startTime: %v, endTime: %v, timeSpan: %v", startTimeConverted, endTimeConverted, timeSpan))
					if overlap(startTimeConverted, endTimeConverted, timeSpan.Start, timeSpan.End) {
						valid = true
					}
					if overlap(startTimeConverted.Add(time.Hour * 24), endTimeConverted.Add(time.Hour * 24), timeSpan.Start, timeSpan.End) {
						valid = true
					}
				}
				if !valid {
					return fmt.Errorf("does not match any timespan in zone: %s", zone)
				}
			}
		}
		unavailableZones := countUnavailableZones(len(task.Zones), zone, task.Priority, startTime, endTime)
		if len(config.WhiteList) - unavailableZones  < config.AvailableZones {
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
			if !task.StartDatetime.Before(schedTaskEnd) {
				startIdx = i + 1
			}
		}
		// no overlaps
		if len(overlaps) == 0 {
			order.reschedTaskIds = []string{}
			order.addIdx = startIdx
			return order, nil
		}
		// there are overlaps
		// check priorities, status of scheduled tasks (if "cancel", then the task is set for cancellation/extension/rescheduling) and status of this task (if change, then this task can reschedule overlaps)
		for _, i := range overlaps {
			schedTask := tasks[zoneSchedule[i]]
			if schedTask.Priority <= task.Priority && schedTask.Status != "cancel" && task.Status != "change" {
				return order, fmt.Errorf("can't schedule task; overlap in zone %s with task with priority %d %s (%s, critical: %v), %v-%v", zone, schedTask.Priority, schedTask.ID, schedTask.Type, schedTask.Critical, schedTask.StartDatetime, schedTask.StartDatetime.Add(schedTask.Duration))
			}
		}
		// no priority overlaps; reschedule with compression or cancel less prioritized overlapping tasks
		for _, i := range overlaps {
			order.reschedTaskIds = append(order.reschedTaskIds, tasks[zoneSchedule[i]].ID)
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

func scheduleTask(task *Task, assignStatus string) error {
	status := task.Status
	task.Status = assignStatus

	err := availableTimeZone(task)
	if err != nil {
		task.Status = status
		return err
	}
	
	err = availableTimespan(task)
	if err != nil {
		task.Status = status
		return err
	}
	task.Status = status

	return nil
}

func reschedule() (errors error) {
	statuses := make(map[string]string)
	for taskID := range tasks {
		statuses[taskID] = tasks[taskID].Status
		cancelTask(taskID)
	}
	for _, task := range tasks {
		if statuses[task.ID] == "wait" {
			err := scheduleTask(task, statuses[task.ID])
			if err != nil {
				cancelTask(task.ID)
				errors = multierr.Append(errors, fmt.Errorf("%s: %w", task.ID,  err))
			} else {
				task.Status = statuses[task.ID]
			}
		}
	}
	return errors
}