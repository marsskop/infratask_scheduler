package main

type AddTaskReq struct {
	PreferredStartDatetime string   `json:"StartDatetime"`
	Duration 				string   `json:"Duration"`
	Deadline 				string   `json:"Deadline"`
	Zones 					[]string `json:"Zones"`
	Type 					string   `json:"Type"` // auto or manual
	Critical 				bool     `json:"Critical"` // only for manual type
}

type ExtendTaskReq struct {
	NewDuration string `json:"Duration"`
}

type MoveTaskReq struct {
	NewStartDateTime string `json:"StartDatetime"`
}

type PrettySchedule struct {
	ID 			string
	StartTime 	string
	EndTime 	string
	Type 		string
	Critical 	bool
}