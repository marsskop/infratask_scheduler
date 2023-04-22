package main

type AddTaskReq struct {
	Name					string	 `json:"Name"`
	StartDatetime 			string   `json:"StartDatetime"`
	PreferredStartDatetime	string   `json:"PrefStartDatetime,omitempty"`  // for suggestions
	Duration 				string   `json:"Duration"`
	Deadline 				string   `json:"Deadline"`
	Zones 					[]string `json:"Zones"`
	Type 					string   `json:"Type"` // auto or manual
	Critical 				bool     `json:"Critical"` // only for manual type
	CompressionPerc			int 	 `json:"CompressionPerc,omitempty"`  // compression percentage for auto
}

type ExtendTaskReq struct {
	NewDuration string `json:"Duration"`
}

type MoveTaskReq struct {
	NewStartDateTime string `json:"StartDatetime"`
}

type PrettySchedule struct {
	Name		string
	ID 			string
	StartTime 	string
	EndTime 	string
	Type 		string
	Critical 	bool
}