// Copyright 2017 Capsule8, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sensor

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sync/atomic"

	"github.com/capsule8/capsule8/pkg/sys"
	"github.com/capsule8/capsule8/pkg/sys/perf"
)

// TelemetryEvent is an interface defining an event generated by the sensor in
// response to activity on the system that matches a subscriptions's event
// filter.
type TelemetryEvent interface {
	CommonTelemetryEventData() TelemetryEventData
}

// TelemetryEventData is an event generated by the sensor in response to activity
// on the system that matches a subscription's event filter. It contains all
// relevant information.
type TelemetryEventData struct {
	EventID        string
	SensorID       string
	MonotimeNanos  int64
	SequenceNumber uint64

	ProcessID      string
	PID            int
	TGID           int
	CPU            uint32
	HasCredentials bool
	Credentials    Cred

	Container ContainerInfo
}

// Init initializes a telemetry event with common sensor-specific fields
// correctly populated.
func (e *TelemetryEventData) Init(sensor *Sensor) {
	e.SensorID = sensor.ID
	e.MonotimeNanos = sys.CurrentMonotonicRaw() - sensor.bootMonotimeNanos
	e.SequenceNumber = atomic.AddUint64(&sensor.sequenceNumber, 1)

	var b []byte
	buf := bytes.NewBuffer(b)
	binary.Write(buf, binary.LittleEndian, sensor.ID)
	binary.Write(buf, binary.LittleEndian, e.SequenceNumber)
	binary.Write(buf, binary.LittleEndian, e.MonotimeNanos)
	hash := sha256.Sum256(buf.Bytes())
	e.EventID = hex.EncodeToString(hash[:])

	atomic.AddUint64(&sensor.Metrics.Events, 1)
}

// InitWithSample initializes a telemetry event using perf_event sample
// information. If the sample should be suppressed for some reason, the
// return will be false.
func (e *TelemetryEventData) InitWithSample(
	sensor *Sensor,
	sample *perf.SampleRecord,
	data perf.TraceEventSampleData,
) bool {
	var (
		ok           bool
		leader, task *Task
	)

	// Avoid the lookup if we've been given the information.
	// This happens most commonly with process events.
	if task, ok = data["__task__"].(*Task); ok {
		leader = task.Leader()
	} else if pid, _ := data["common_pid"].(int32); pid != 0 {
		// When both the sensor and the process generating the sample
		// are in containers, the sample.Pid and sample.Tid fields will
		// be zero. Use "common_pid" from the trace event data instead.
		task, leader = sensor.ProcessCache.LookupTaskAndLeader(int(pid))
	}

	e.Init(sensor)
	e.MonotimeNanos = int64(sample.Time) - sensor.bootMonotimeNanos
	e.CPU = sample.CPU

	if task != nil {
		e.ProcessID = task.ProcessID
		e.PID = task.PID
		e.TGID = task.TGID
		if task.Creds != nil {
			e.HasCredentials = true
			e.Credentials = *task.Creds
		}

		if i := sensor.ProcessCache.LookupTaskContainerInfo(leader); i == nil {
			e.Container.ID = task.ContainerID
		} else {
			e.Container = *i
		}
	}

	// Return false if the event comes from the sensor itself
	return leader == nil || !leader.IsSensor()
}