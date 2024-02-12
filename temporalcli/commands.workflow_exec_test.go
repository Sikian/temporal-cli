package temporalcli_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strconv"

	"github.com/google/uuid"
	"go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/protobuf/proto"
)

// TODO(cretz): To test:
// * Start failure
// * Execute failure on start
// * Execute workflow failure (including nested failures)
// * Execute workflow cancel
// * Execute workflow timeout
// * Execute workflow continue as new
// * Workflow with proto JSON input

func (s *SharedServerSuite) TestWorkflow_Start_SimpleSuccess() {
	// Text
	s.Worker.OnDevWorkflow(func(ctx workflow.Context, input any) (any, error) {
		return map[string]string{"foo": "bar"}, nil
	})
	res := s.Execute(
		"workflow", "start",
		"--address", s.Address(),
		"--task-queue", s.Worker.Options.TaskQueue,
		"--type", "DevWorkflow",
		"--workflow-id", "my-id1",
	)
	s.NoError(res.Err)
	// Confirm text has key/vals as expected
	out := res.Stdout.String()
	s.ContainsOnSameLine(out, "WorkflowId", "my-id1")
	s.Contains(out, "RunId")
	s.ContainsOnSameLine(out, "TaskQueue", s.Worker.Options.TaskQueue)
	s.ContainsOnSameLine(out, "Type", "DevWorkflow")
	s.ContainsOnSameLine(out, "Namespace", "default")

	// JSON
	res = s.Execute(
		"workflow", "start",
		"-o", "json",
		"--address", s.Address(),
		"--task-queue", s.Worker.Options.TaskQueue,
		"--type", "DevWorkflow",
		"--workflow-id", "my-id2",
	)
	s.NoError(res.Err)
	var jsonOut map[string]string
	s.NoError(json.Unmarshal(res.Stdout.Bytes(), &jsonOut))
	s.Equal("my-id2", jsonOut["workflowId"])
	s.NotEmpty(jsonOut["runId"])
	s.Equal(s.Worker.Options.TaskQueue, jsonOut["taskQueue"])
	s.Equal("DevWorkflow", jsonOut["type"])
	s.Equal("default", jsonOut["namespace"])
}

func (s *SharedServerSuite) TestWorkflow_Execute_SimpleSuccess() {
	// Text
	s.Worker.OnDevWorkflow(func(ctx workflow.Context, input any) (any, error) {
		return map[string]string{"foo": "bar"}, nil
	})
	res := s.Execute(
		"workflow", "execute",
		"--address", s.Address(),
		"--task-queue", s.Worker.Options.TaskQueue,
		"--type", "DevWorkflow",
		"--workflow-id", "my-id1",
		"-i", `["val1", "val2"]`,
	)
	s.NoError(res.Err)
	out := res.Stdout.String()
	// Confirm running (most of this check is done on start test)
	s.ContainsOnSameLine(out, "WorkflowId", "my-id1")
	s.Equal([]any{"val1", "val2"}, s.Worker.DevWorkflowLastInput())
	// Confirm we have some events
	s.ContainsOnSameLine(out, "1", "WorkflowExecutionStarted")
	s.ContainsOnSameLine(out, "2", "WorkflowTaskScheduled")
	s.ContainsOnSameLine(out, "3", "WorkflowTaskStarted")
	// Confirm results
	s.Contains(out, "RunTime")
	s.ContainsOnSameLine(out, "Status", "COMPLETED")
	s.ContainsOnSameLine(out, "Result", `{"foo":"bar"}`)

	// JSON
	res = s.Execute(
		"workflow", "execute",
		"-o", "json",
		"--address", s.Address(),
		"--task-queue", s.Worker.Options.TaskQueue,
		"--type", "DevWorkflow",
		"--workflow-id", "my-id2",
	)
	s.NoError(res.Err)
	var jsonOut map[string]any
	s.NoError(json.Unmarshal(res.Stdout.Bytes(), &jsonOut))
	s.Equal("my-id2", jsonOut["workflowId"])
	s.Equal("COMPLETED", jsonOut["status"])
	s.NotNil(jsonOut["closeEvent"])
	s.Equal(map[string]any{"foo": "bar"}, jsonOut["result"])
}

func (s *SharedServerSuite) TestWorkflow_Execute_SimpleFailure() {
	// Text
	s.Worker.OnDevWorkflow(func(ctx workflow.Context, input any) (any, error) {
		return nil, fmt.Errorf("intentional failure")
	})
	res := s.Execute(
		"workflow", "execute",
		"--address", s.Address(),
		"--task-queue", s.Worker.Options.TaskQueue,
		"--type", "DevWorkflow",
		"--workflow-id", "my-id1",
	)
	s.ErrorContains(res.Err, "workflow failed")
	out := res.Stdout.String()
	// Confirm failure
	s.ContainsOnSameLine(out, "Status", "FAILED")
	s.Contains(out, "Failure")
	s.ContainsOnSameLine(out, "Message", "intentional failure")

	// JSON
	res = s.Execute(
		"workflow", "execute",
		"-o", "json",
		"--address", s.Address(),
		"--task-queue", s.Worker.Options.TaskQueue,
		"--type", "DevWorkflow",
		"--workflow-id", "my-id2",
	)
	s.ErrorContains(res.Err, "workflow failed")
	var jsonOut map[string]any
	s.NoError(json.Unmarshal(res.Stdout.Bytes(), &jsonOut))
	s.Equal("FAILED", jsonOut["status"])
	s.Equal("intentional failure",
		jsonPath(jsonOut, "closeEvent", "workflowExecutionFailedEventAttributes", "failure", "message"))
}

func (s *SharedServerSuite) TestWorkflow_Execute_EnvVars() {
	s.CommandHarness.Options.LookupEnv = func(key string) (string, bool) {
		if key == "TEMPORAL_ADDRESS" {
			return s.Address(), true
		}
		return "", false
	}
	res := s.Execute(
		"workflow", "execute",
		"--task-queue", s.Worker.Options.TaskQueue,
		"--type", "DevWorkflow",
		"--workflow-id", "my-id1",
	)
	s.NoError(res.Err)
}

func (s *SharedServerSuite) TestWorkflow_Execute_EnvConfig() {
	// Temp file for env
	tmpFile, err := os.CreateTemp("", "")
	s.NoError(err)
	// s.CommandHarness.Options.EnvConfigFile = tmpFile.Name()
	defer os.Remove(tmpFile.Name())

	// Set config value for input (obviously `--input` is normally a poor choice
	// for an env file)
	res := s.Execute(
		"env", "set",
		"--env-file", tmpFile.Name(),
		"myenv.input", `"env-conf-input"`,
	)
	s.NoError(res.Err)

	// Command with its own input which overrides env
	res = s.Execute(
		"workflow", "execute",
		"--env", "myenv",
		"--env-file", tmpFile.Name(),
		"--address", s.Address(),
		"--task-queue", s.Worker.Options.TaskQueue,
		"--type", "DevWorkflow",
		"--workflow-id", "my-id1",
		"--input", `"cli-input"`,
	)
	s.NoError(res.Err)
	s.ContainsOnSameLine(res.Stdout.String(), "Result", `"cli-input"`)

	// But if command does not have input, can use env's
	res = s.Execute(
		"workflow", "execute",
		"--env", "myenv",
		"--env-file", tmpFile.Name(),
		"--address", s.Address(),
		"--task-queue", s.Worker.Options.TaskQueue,
		"--type", "DevWorkflow",
		"--workflow-id", "my-id2",
	)
	s.NoError(res.Err)
	s.ContainsOnSameLine(res.Stdout.String(), "Result", `"env-conf-input"`)
}

func (s *SharedServerSuite) TestWorkflow_Execute_CodecEndpoint() {
	// Start HTTP server for our codec
	srv := httptest.NewServer(converter.NewPayloadCodecHTTPHandler(prefixingCodec{}))
	defer srv.Close()

	// Run a different worker than the suite on a different task queue that has
	// our codec
	prefixedDataConverter := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), prefixingCodec{})
	workerClient, err := client.NewClientFromExisting(s.Client, client.Options{DataConverter: prefixedDataConverter})
	s.NoError(err)
	defer workerClient.Close()
	taskQueue := uuid.NewString()
	worker := worker.New(workerClient, taskQueue, worker.Options{})
	worker.RegisterWorkflowWithOptions(
		func(ctx workflow.Context, arg any) (any, error) { return arg, nil },
		workflow.RegisterOptions{Name: "test-workflow"},
	)
	s.NoError(worker.Start())
	defer worker.Stop()

	// Helper to confirm encoded
	assertWorkflowEncoded := func(workflowID string) {
		var foundStart, foundComplete bool
		iter := s.Client.GetWorkflowHistory(s.Context, workflowID, "", false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
		for iter.HasNext() {
			event, err := iter.Next()
			s.NoError(err)
			if start := event.GetWorkflowExecutionStartedEventAttributes(); start != nil {
				foundStart = true
				s.Equal("binary/prefixed", string(start.Input.Payloads[0].Metadata["encoding"]))
			} else if complete := event.GetWorkflowExecutionCompletedEventAttributes(); complete != nil {
				foundComplete = true
				s.Equal("binary/prefixed", string(complete.Result.Payloads[0].Metadata["encoding"]))
			}
		}
		s.True(foundStart)
		s.True(foundComplete)
	}

	// Run a workflow with our codec endpoint
	res := s.Execute(
		"workflow", "execute",
		"--codec-endpoint", "http://"+srv.Listener.Addr().String(),
		"--address", s.Address(),
		"--task-queue", taskQueue,
		"--type", "test-workflow",
		"--workflow-id", "my-id1",
		"--input", `{"foo":"bar"}`,
	)
	s.NoError(res.Err)
	// Confirm result is proper, but when fetching history both input and result
	// are mangled
	s.ContainsOnSameLine(res.Stdout.String(), "Result", `{"foo":"bar"}`)
	assertWorkflowEncoded("my-id1")

	// Let's do the same with JSON and full details so we can check history is
	// actually decoded for the user
	res = s.Execute(
		"workflow", "execute",
		"-o", "json", "--event-details",
		"--codec-endpoint", "http://"+srv.Listener.Addr().String(),
		"--address", s.Address(),
		"--task-queue", taskQueue,
		"--type", "test-workflow",
		"--workflow-id", "my-id2",
		"--input", `{"foo":"bar"}`,
	)
	s.NoError(res.Err)
	assertWorkflowEncoded("my-id2")
	var jsonOut map[string]any
	s.NoError(json.Unmarshal(res.Stdout.Bytes(), &jsonOut))
	s.Equal("bar", jsonPath(jsonOut, "result", "foo"))
	input, err := base64.StdEncoding.DecodeString(jsonPath(jsonOut,
		"history", "events", "0", "workflowExecutionStartedEventAttributes", "input", "payloads", "0", "data").(string))
	s.NoError(err)
	s.Equal(`{"foo":"bar"}`, string(input))

	// Run without codec endpoint and confirm remains encoded for user
	res = s.Execute(
		"workflow", "execute",
		"--address", s.Address(),
		"--task-queue", taskQueue,
		"--type", "test-workflow",
		"--workflow-id", "my-id3",
		"--input", `{"foo":"bar"}`,
	)
	s.NoError(res.Err)
	s.ContainsOnSameLine(res.Stdout.String(), "Result",
		fmt.Sprintf("%q:%q", "encoding", base64.StdEncoding.EncodeToString([]byte("binary/prefixed"))))
}

type prefixingCodec struct{}

func (prefixingCodec) Encode(payloads []*common.Payload) ([]*common.Payload, error) {
	ret := make([]*common.Payload, len(payloads))
	for i, payload := range payloads {
		ret[i] = proto.Clone(payload).(*common.Payload)
		ret[i].Data = append([]byte("prefix-"), ret[i].Data...)
		ret[i].Metadata["old-encoding"] = ret[i].Metadata["encoding"]
		ret[i].Metadata["encoding"] = []byte("binary/prefixed")
	}
	return ret, nil
}

func (prefixingCodec) Decode(payloads []*common.Payload) ([]*common.Payload, error) {
	ret := make([]*common.Payload, len(payloads))
	for i, payload := range payloads {
		ret[i] = proto.Clone(payload).(*common.Payload)
		if bytes.HasPrefix(ret[i].Data, []byte("prefix-")) {
			ret[i].Data = bytes.TrimPrefix(ret[i].Data, []byte("prefix-"))
			ret[i].Metadata["encoding"] = ret[i].Metadata["old-encoding"]
			delete(ret[i].Metadata, "old-encoding")
		}
	}
	return ret, nil
}

func jsonPath(v any, path ...string) any {
	switch t := v.(type) {
	case map[string]any:
		v = t[path[0]]
	case []any:
		i, err := strconv.Atoi(path[0])
		if err != nil {
			panic(err)
		}
		v = t[i]
	default:
		panic(fmt.Sprintf("unknown type: %T", v))
	}
	if len(path) == 1 {
		return v
	}
	return jsonPath(v, path[1:]...)
}