package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"time"
	"encoding/json"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/google/uuid"
)

type Durations struct {
	MinAutoDuration time.Duration `mapstructure:"minAutoDuration"`
	MinManualDuration time.Duration `mapstructure:"minManualDuration"`
	MaxNoncritDuration time.Duration `mapstructure:"maxNoncritDuration"`
	MaxCritDuration time.Duration `mapstructure:"maxCritDuration"`
	DeadlineDuration time.Duration `mapstructure:"deadlineDuration"`
	PreferredManualStartMult time.Duration `mapstructure:"preferredManualStartMult"`
	PreferredAutoStartMult time.Duration `mapstructure:"preferredAutoStartMult"`
}

var durations Durations

type Config struct {
	WhiteListRaw	map[string][]string	`mapstructure:"whiteList"`
	WhiteList 		map[string][]timeSpan `mapstructure:"-"`
	BlackList 		[]string `mapstructure:"blackList"`
	AvailableZones 	int `mapstructure:"availableZones"`
	Pauses 			map[string]time.Duration `mapstructure:"pauses"`
}

type timeSpan struct {
	Start 	time.Time
	End 	time.Time
}

var config Config

func loadWhiteList() error {
	whiteList := make(map[string][]timeSpan)
	for key, zoneSpans := range config.WhiteListRaw {
		var timeSpans []timeSpan
		for _, timeSpanString := range zoneSpans {
			timeSpanSlice := strings.Split(timeSpanString, "-")
			start, err := time.Parse("15:04", timeSpanSlice[0])
			if err != nil {
				return fmt.Errorf("configuration error:%s", err.Error())
			}
			end, err := time.Parse("15:04", timeSpanSlice[1])
			if err != nil {
				return fmt.Errorf("configuration error:%s", err.Error())
			}
			if start.After(end) {
				end = end.Add(time.Hour * 24)
			}
			timeSpans = append(timeSpans, timeSpan{
				Start: start,
				End: end,
			})
		}
		whiteList[key] = timeSpans
	}
	config.WhiteList = whiteList
	return nil
}

func addTask(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	var addTaskReq AddTaskReq
	reqBody, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Warn(err)
		return
	}
	json.Unmarshal(reqBody, &addTaskReq)

	// time conversion and validation
	startDatetime, err := time.Parse("02/01/2006 15:04", addTaskReq.PreferredStartDatetime)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Warn(err)
		return
	}
	duration, err := time.ParseDuration(addTaskReq.Duration)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Warn(err)
		return
	}
	if startDatetime.Before(time.Now()) || startDatetime.Add(duration).Before(time.Now()) {
		err = fmt.Errorf("can't set tasks in the past")
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Warn(err)
		return
	}
	deadline, err := time.Parse("02/01/2006 15:04", addTaskReq.Deadline)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Warn(err)
		return
	}
	if deadline.Before(startDatetime) || deadline.Before(startDatetime.Add((duration))) {
		err = fmt.Errorf("can't set deadline earlier than task ends %v", deadline)
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Warn(err)
		return
	}
	if time.Now().Add(durations.DeadlineDuration).Before(deadline) {
		err = fmt.Errorf("can't set deadline longer than %v", durations.DeadlineDuration)
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Warn(err)
		return
	}
	if addTaskReq.Type != "auto" && addTaskReq.Type != "manual" {
		err = fmt.Errorf("unknown type of task")
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Warn(err)
		return
	}
	if addTaskReq.Type == "auto" && addTaskReq.Critical {
		err = fmt.Errorf("auto tasks can't be critical")
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Warn(err)
		return
	}

	taskID := uuid.New().String()
	task := Task{
		ID: taskID,
		StartDatetime: startDatetime,
		Duration: duration,
		Deadline: deadline,
		Zones: addTaskReq.Zones,
		Type: addTaskReq.Type,
		Critical: addTaskReq.Critical,
		Priority: priorityRule(addTaskReq.Type, addTaskReq.Critical),
		Status: "wait",
	}
	err = scheduleTask(&task)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Warn(err)
		return
	}

	tasks[task.ID] = &task
	w.WriteHeader(http.StatusCreated)

	json.NewEncoder(w).Encode(task)
	log.Info("Added task ", task.ID)
}

func listTasks(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

func showSchedule(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	scheduleResp := make(map[string][]PrettySchedule)
	for zone, scheduleZone := range schedule {
		scheduleResp[zone] = []PrettySchedule{}
		for _, taskId := range scheduleZone {
			prettySchedule := PrettySchedule {
				ID: taskId,
				StartTime: tasks[taskId].StartDatetime.Format("15:04 02/01/2006"),
				EndTime: tasks[taskId].StartDatetime.Add(tasks[taskId].Duration).Format("15:04 02/01/2006"),
				Type: tasks[taskId].Type,
				Critical: tasks[taskId].Critical,
			}
			scheduleResp[zone] = append(scheduleResp[zone], prettySchedule)
		}
	}
	json.NewEncoder(w).Encode(scheduleResp)
}

func getTask(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	taskID := mux.Vars(r)["uuid"]
	task, ok := tasks[taskID]
	if ok {
		json.NewEncoder(w).Encode(task)
		return
	}
	http.Error(w, fmt.Sprintf("No task with this ID %s", taskID), http.StatusBadRequest)
}

func deleteTask(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	taskID := mux.Vars(r)["uuid"]
	_, ok := tasks[taskID]
	if ok {
		cancelTask(taskID)
		w.WriteHeader(http.StatusOK)
		log.Info("Cancelled task ", taskID)
		return
	}
	http.Error(w, fmt.Sprintf("No task with this ID %s", taskID), http.StatusBadRequest)
}

func extendTask(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	var extendTaskReq ExtendTaskReq
	reqBody, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Warn(err)
		return
	}
	json.Unmarshal(reqBody, &extendTaskReq)

	// time conversion and validation
	newDuration, err := time.ParseDuration(extendTaskReq.NewDuration)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Warn(err)
		return
	}

	taskID := mux.Vars(r)["uuid"]
	task, ok := tasks[taskID]
	if ok {
		if task.Type == "manual" && task.Status == "progress" {
			if newDuration < task.Duration {
				http.Error(w, "Can only extend tasks", http.StatusBadRequest)
				log.Warn("Can only extend tasks ", taskID)
				return
			}
			task.Duration = newDuration
			err := scheduleTask(task)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				log.Warn(err)
				return
			}

			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(task)
			log.Info("Extended task %s", taskID)
			return
		} else {
			http.Error(w, "Can only extend manual tasks in progress", http.StatusBadRequest)
			log.Warn("Can only extend manual tasks in progress", taskID)
			return
		}
	}
	http.Error(w, fmt.Sprintf("No task with this ID %s", taskID), http.StatusBadRequest)
}

func moveTask(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	var moveTaskReq MoveTaskReq
	reqBody, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Warn(err)
		return
	}
	json.Unmarshal(reqBody, &moveTaskReq)

	// time conversion and validation
	newStartDatetime, err := time.Parse("02/01/2006 15:04", moveTaskReq.NewStartDateTime)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Warn(err)
		return
	}
	if newStartDatetime.Before(time.Now()) {
		err = fmt.Errorf("can't set tasks in the past")
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Warn(err)
		return
	}

	taskID := mux.Vars(r)["uuid"]
	task, ok := tasks[taskID]
	if ok {
		if task.Status != "wait" {
			http.Error(w, "Can only move tasks in wait", http.StatusBadRequest)
			log.Warn("Can only move tasks in wait", taskID)
			return
		}
		startDatetime := task.StartDatetime
		task.StartDatetime = newStartDatetime
		err := scheduleTask(task)
		if err != nil {
			task.StartDatetime = startDatetime
			http.Error(w, err.Error(), http.StatusBadRequest)
			log.Warn(err)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(task)
		log.Info("Moved task %s", taskID)
		return
	}
	http.Error(w, "No task with this ID", http.StatusBadRequest)
}

func loggingMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        log.Debug(fmt.Sprintf("Received %s request to %s", r.Method, r.RequestURI))
        next.ServeHTTP(w, r)
    })
}

func main() {
	debug := flag.Bool("debug", false, "Debug mode")
	port := flag.Int("port", 8080, "Server port")
	configsDir := flag.String("configs", "./configs", "Configurations directory")
	flag.Parse()
	if *debug {
		log.SetLevel(log.DebugLevel)
		log.Debug("Log level set to debug")
	}

	// load durations config
	viper.AddConfigPath(*configsDir)
	viper.SetConfigName("durations")
	viper.SetConfigType("yaml")
	if err := viper.ReadInConfig(); err != nil {
		log.Fatal(err)

	}
	err := viper.Unmarshal(&durations)
	if err != nil {
		log.Fatal(err)
	}
	log.Debug("Durations config loaded:\n", durations)

	// load common reloadable config
	viper.AddConfigPath(*configsDir)
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	if err := viper.ReadInConfig(); err != nil {
		log.Fatal(err)

	}
	err = viper.Unmarshal(&config)
	if err != nil {
		log.Fatal(err)
	}
	err = loadWhiteList()
	if err != nil {
		log.Fatal(err)
	}
	log.Debug("Config loaded:\n", config)
	viper.WatchConfig()  // watches only the last config
	viper.OnConfigChange(func(e fsnotify.Event) {
		log.Info("Config file changed:", e.Name)
		err = viper.Unmarshal(&config)
		if err != nil {
			log.Fatal(err)
		}
		err = loadWhiteList()
		if err != nil {
			log.Fatal(err)
		}
		log.Debug("Config loaded:\n", config)
		err = reschedule()
		if err != nil {
			log.Warn(fmt.Sprintf("Rescheduling errors: %s", err.Error()))
		}
	})

	router := mux.NewRouter()
	router.Path("/tasks").Methods("POST").HandlerFunc(addTask)
	router.Path("/tasks").Methods("GET").HandlerFunc(listTasks)
	router.Path("/schedule").Methods("GET").HandlerFunc(showSchedule)
	router.Path("/tasks/{uuid}").Methods("GET").HandlerFunc(getTask)
	router.Path("/tasks/{uuid}").Methods("DELETE").HandlerFunc(deleteTask)
	router.Path("/tasks/extend/{uuid}").Methods("PUT").HandlerFunc(extendTask)
	router.Path("/tasks/move/{uuid}").Methods("PUT").HandlerFunc(moveTask)
	router.Use(loggingMiddleware)

	srv := &http.Server{
		Handler: router,
		Addr: fmt.Sprintf("0.0.0.0:%d", *port),
		WriteTimeout: 15 * time.Second,
		ReadTimeout: 15 * time.Second,
		IdleTimeout:  time.Second * 60,
	}

	log.Info(fmt.Sprintf("Starting HTTP server on 0.0.0.0:%d...", *port))
	go func() {
		if err := srv.ListenAndServe(); err != nil {
			log.Warn(err)
		}
	}()
	c := make(chan os.Signal, 1)
    signal.Notify(c, os.Interrupt) // quit via SIGINT (Ctrl+C)
    <-c
    ctx, cancel := context.WithTimeout(context.Background(), time.Second * 15)
    defer cancel()
    srv.Shutdown(ctx) // graceful shutdown
    log.Info("Shutting down...")
    os.Exit(0)
}