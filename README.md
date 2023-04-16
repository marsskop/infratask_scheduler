# infratask_scheduler

Scheduler for inftrastructure tasks. Done for [IT's Tinkoff Solution Cup](https://www.tinkoff.ru/solutioncup/).

## Key Features
- upload and schedule tasks depending on their priority (critical - 0, manual noncritical - 1, auto -2)
- cancel tasks
- extend tasks duration
- move tasks

## How to Run
### Run in Docker
- build Docker image
```bash
docker build -t marskop/infratasksch
```
- run in Docker
```bash
docker run -d -v $(pwd)/configs:/tmp/configs -p 8080:8080 marskop/infratasksch /usr/local/bin/infratasksch
```

### Run from Source
> 🔔 Make sure that you have [downloaded](https://go.dev/dl/) and installed **Go**. Version 1.18 or higher is required.
```bash
go run *.go
```

### Configuration
- Durations Config (`/configs/durations.yaml`)
```yaml
minAutoDuration: 5m
minManualDuration: 30m
maxNoncritDuration: 6h
maxCritDuration: null
deadlineDuration: 672h
preferredManualStartMult: 5m
preferredAutoStartMult: 1m
```
Options are:
> set as `time.Duration` format or `null`
- **minAutoduration**: minimal task duration for automatic tasks
- **minManualDuration**: minimal task duration for manual tasks
- **maxNoncritDuration**: max task duration for noncritical tasks
- **maxCritDuration**: max task duration for critical tasks
- **deadlineDuration**: max deadline duration
- **preferredManualStartMult**: manual start time should be multiplicated by this value
- **preferredAutoStartMult**: manual start time should be multiplicated by this value
- Common Config (reloadable) (`/configs/config.yaml`)
```yaml
whiteList:
  dev1:
  - 00:00-23:59
  dev2:
  - 00:00-06:00
  - 22:00-23:59
  dev3:
  - 00:00-04:00
blackList:
- prod1
- prod2
- preprod1
availableZones: 2
pauses:
  dev1: 5m
  dev2: 5m
  dev3: 10m
  prod1: 30m
  prod2: 60m
  preprod1: 30m
```
Options are:
- **whiteList**: map of lists of timespans for tasks in zones
- **blackList**: list of zones in which only critical tasks can be run
- **availableZones**: number of zones that don't have any tasks at any time
- **pauses**: map of pauses between tasks in zone


## API Endpoints
- `GET /tasks`: returns list of tasks without schedule, cancelled tasks too.

Example response:
```json
{
    "49a0fdc3-ad1a-49b1-9160-b389f2c83003": {
        "ID": "49a0fdc3-ad1a-49b1-9160-b389f2c83003",
        "StartDatetime": "2023-04-17T22:51:00Z",
        "Duration": 7200000000000,
        "Deadline": "2023-04-26T00:00:00Z",
        "Zones": [
            "dev1"
        ],
        "Type": "manual",
        "Critical": false,
        "Priority": 1,
        "Status": "cancel"
    },
    "abee1e20-9c17-43dc-a9c4-72b645f99618": {
        "ID": "abee1e20-9c17-43dc-a9c4-72b645f99618",
        "StartDatetime": "2023-04-17T22:51:00Z",
        "Duration": 7200000000000,
        "Deadline": "2023-04-26T00:00:00Z",
        "Zones": [
            "dev1",
            "prod1"
        ],
        "Type": "manual",
        "Critical": true,
        "Priority": 0,
        "Status": "wait"
    }
}
```
- `POST /tasks`: add new task.

Example request:
```json
{
    "StartDatetime": "17/04/2023 02:15",
    "Duration": "4h",
    "Deadline": "26/04/2023",
    "Zones": ["dev1"],
    "Type": "manual",
    "Critical": true
}
```
Example response:
```json
{
    "ID": "36224d9f-16ba-4847-9dc2-26321bdc3aec",
    "StartDatetime": "2023-04-17T02:15:00Z",
    "Duration": 14400000000000,
    "Deadline": "2023-04-26T00:00:00Z",
    "Zones": [
        "dev1"
    ],
    "Type": "manual",
    "Critical": true,
    "Priority": 0,
    "Status": "wait"
}
```
Or a message as to why this task can't be scheduled:
```
can't schedule task; overlap in zone dev1 with task with priority 0 140676e2-257d-4fe6-aaf6-4e883ead93a9 (manual, critical: true), 2023-04-17 00:20:00 +0000 UTC-2023-04-17 02:20:00 +0000 UTC
```

- `GET /schedule`: returns ordered schedule by zones.

Example response: 
```json
{
    "dev1": [
        {
            "ID": "36224d9f-16ba-4847-9dc2-26321bdc3aec",
            "StartTime": "02:15 17/04/2023",
            "EndTime": "06:15 17/04/2023",
            "Type": "manual",
            "Critical": true
        },
        {
            "ID": "404249ba-93bd-4c65-9002-9dc61a359743",
            "StartTime": "00:15 19/04/2023",
            "EndTime": "04:15 19/04/2023",
            "Type": "auto",
            "Critical": false
        }
    ],
    "dev2": [
        {
            "ID": "b0ac4cf6-1560-4a61-b9f6-06e1efc0f4fa",
            "StartTime": "00:15 18/04/2023",
            "EndTime": "04:15 18/04/2023",
            "Type": "manual",
            "Critical": true
        }
    ]
}
```
- `GET /tasks/{taskID}`: get information on a task.

Example response: 
```json
{
    "ID": "36224d9f-16ba-4847-9dc2-26321bdc3aec",
    "StartDatetime": "2023-04-17T02:15:00Z",
    "Duration": 14400000000000,
    "Deadline": "2023-04-26T00:00:00Z",
    "Zones": [
        "dev1"
    ],
    "Type": "manual",
    "Critical": true,
    "Priority": 0,
    "Status": "wait"
}
```
- `DELETE /tasks/{taskID}`: cancels task. Successful response is `Status 200`.
- `PUT /tasks/extend/{taskID}`: extends task with new duration.

Example request:
```json
{
    "Duration": "3h"
}
```

Example response:
```json
{
    "ID": "40ef3a8c-2e94-46f8-b706-da343b50824d",
    "StartDatetime": "2023-04-17T00:51:00Z",
    "Duration": 7200000000000,
    "Deadline": "2023-04-16T00:00:00Z",
    "Zones": [
        "prod1",
        "dev2"
    ],
    "Type": "manual",
    "Critical": true,
    "Priority": 0,
    "Status": "wait"
}
```


- `PUT /tasks/move/{taskID}`: moves task to another start time.

Example request:
```json
{
    "StartDatetime": "18/04/2023 00:51"
}
```

Example response:
```json
{
    "ID": "404249ba-93bd-4c65-9002-9dc61a359743",
    "StartDatetime": "2023-04-18T00:51:00Z",
    "Duration": 14400000000000,
    "Deadline": "2023-04-26T00:00:00Z",
    "Zones": [
        "dev1"
    ],
    "Type": "auto",
    "Critical": false,
    "Priority": 2,
    "Status": "wait"
}
```

## ⚠️  License
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

Code is owned by Tinkoff.