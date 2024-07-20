package mr

//
// RPC definitions.
//
// remember to capitalize all names.
//

import "os"
import "strconv"

//
// example to show how to declare the arguments
// and reply for an RPC.
//

type ExampleArgs struct {
	X int
}

// this rpc is for workers to get task from coordinator
type GetTaskRequest struct {
	Worker int
	
}

type ExampleReply struct {
	Y int
}

// this response is for coordinators to return task to the worker
type GetTaskResponse struct {
	Task *Task
}

// Add your RPC definitions here.
// this request is for worker to tell the coordinator that it has finished
// the task it was assigned to
type TaskDoneOrNotRequest struct {
	Task *Task
}

type TaskDoneOrNotReply struct {
	// coordinator does not reply
}

// Cook up a unique-ish UNIX-domain socket name
// in /var/tmp, for the coordinator.
// Can't use the current directory since
// Athena AFS doesn't support UNIX-domain sockets.
func coordinatorSock() string {
	s := "/var/tmp/5840-mr-"
	s += strconv.Itoa(os.Getuid())
	return s
}
