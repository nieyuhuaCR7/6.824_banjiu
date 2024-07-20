package mr

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"sync"
	"time"
)

type phase int

const (
	duringMap phase = iota
	duringReduce
	finish
)

const (
	TaskTimeout = 10 * time.Second // timeout time for task
)

// define what kind of task it is
type TaskType int

const (
	_ TaskType = iota // 0 is unknown
	MapTask // 1: this task is Map task
	ReduceTask // 2: this task is reduce task
	AllTasksDone // 3: all of the tasks are finished, the worker should exit
)

type TaskState int

const (
    Idle TaskState = iota
    InProgress
    Completed
)

type Task struct {
    Type TaskType
	FileNames []string
	Id int
	StartTime time.Time
	State TaskState
	NMap int
	NReduce int
}


type Coordinator struct {
    mu            	sync.Mutex
    mapTasks      	[]*Task
	reduceTasks   	[]*Task
	nMap          	int
	nReduce       	int
	phase        	phase
    finishedMap   	int
	finishedReduce 	int
    // ... others
}

// Your code here -- RPC handlers for the worker to call.

//
// an example RPC handler.
//
// the RPC argument and reply types are defined in rpc.go.
//
func (c *Coordinator) Example(args *ExampleArgs, reply *ExampleReply) error {
	reply.Y = args.X + 1
	return nil
}

// func (c *Coordinator) HandleGetTaskRequest(args *GetTaskRequest, reply *GetTaskResponse) error {
// 	// reply.FileName = c.task.fileName
// 	return nil
// }

func (c *Coordinator) HandleGetTaskRequest(args *GetTaskRequest, reply *GetTaskResponse) error {
    c.mu.Lock()
    defer c.mu.Unlock()

    if c.phase == duringMap {
		reply.Task = findAvailableTask(c.mapTasks)
	} else if c.phase == duringReduce {
		reply.Task = findAvailableTask(c.reduceTasks)
	} else {
		reply.Task = &Task{Type: AllTasksDone}
	}

    return nil
}

func findAvailableTask(tasks []*Task) *Task {
	for _, task := range tasks {
		if task.State == Idle || (task.State == InProgress && time.Since(task.StartTime) > TaskTimeout) {
			task.StartTime = time.Now()
			task.State = InProgress
			// log.Printf("Coordinator: Assigning task: %+v\n", task)
			return task
		}
	}
	return nil
}

// GenerateReduceTasks generates the Reduce tasks after all Map tasks are completed.
func (c *Coordinator) GenerateReduceTasksWithLock() {
	// c.mu.Lock()
	// defer c.mu.Unlock()

	if c.phase != duringReduce {
		return
	}

	// Create Reduce tasks
	for i := 0; i < c.nReduce; i++ {
		c.reduceTasks = append(c.reduceTasks, &Task{
			Type:      ReduceTask,
			Id:        i,
			FileNames: nil, // Will be filled later
			NMap:      len(c.mapTasks),
			NReduce:   c.nReduce,
			State:     Idle,
		})
	}

	c.phase = duringReduce
}

// AssignReduceTasks assigns the files for each Reduce task.
func (c *Coordinator) AssignReduceTasksWithLock() {

	// Assuming intermediate files are named as "mr-x-y.tmp"
	for i := 0; i < c.nReduce; i++ {
		task := c.reduceTasks[i]
		for j := 0; j < len(c.mapTasks); j++ {
			intermediateFile := fmt.Sprintf("./intermediates/mr-%d-%d", j, i)
			task.FileNames = append(task.FileNames, intermediateFile)
		}
		task.State = Idle
	}
}

func (c *Coordinator) TaskDone(args *TaskDoneOrNotRequest, reply *TaskDoneOrNotReply) error {
	if args.Task.Type == MapTask {
		c.TaskDoneMap(args, reply)
	} else if args.Task.Type == ReduceTask {
		c.TaskDoneReduce(args, reply)
	}
	return nil
}

func (c *Coordinator) TaskDoneMap(args *TaskDoneOrNotRequest, reply *TaskDoneOrNotReply) error {
    c.mu.Lock()
    defer c.mu.Unlock()

    // Mark the task as completed
    for _, task := range c.mapTasks {
        if task.Id == args.Task.Id {
            task.State = Completed
			// log.Printf("Coordinator: Task completed: %+v\n", task) // Print the task completion
            break
        }
    }

    // Add logic to move to the next phase if all map tasks are completed
    allMapTasksCompleted := true
    for _, task := range c.mapTasks {
        if task.State != Completed {
            allMapTasksCompleted = false
            break
        }
    }

    if allMapTasksCompleted {
        // Move to the reduce phase
        c.phase = duringReduce
		log.Printf("Coordinator: all map tasks completed, change to next phase: %v\n", c.phase)
        c.GenerateReduceTasksWithLock()
		// add all the reduce filenames
		c.AssignReduceTasksWithLock()
		// this function is for debugging
        // c.PrintReduceTasksWithLock()
	}

    return nil
}

func (c *Coordinator) TaskDoneReduce(args *TaskDoneOrNotRequest, reply *TaskDoneOrNotReply) error {
    c.mu.Lock()
    defer c.mu.Unlock()

    // Mark the task as completed
    for _, task := range c.reduceTasks {
        if task.Id == args.Task.Id {
            task.State = Completed
			// log.Printf("Coordinator: Reduce Task completed: %+v\n", task) // Print the task completion
            break
        }
    }

    // Check if all reduce tasks are completed
    allReduceTasksCompleted := true
    for _, task := range c.reduceTasks {
        if task.State != Completed {
            allReduceTasksCompleted = false
            break
        }
    }

    if allReduceTasksCompleted {
        // All tasks are completed
        c.phase = finish
		log.Printf("Coordinator: All reduce tasks completed, change to finish phase")
    }

    return nil
}

// this function is for debugging
func (c *Coordinator) PrintReduceTasksWithLock() {

    log.Printf("Coordinator: Current reduce tasks: \n")
    for _, task := range c.reduceTasks {
        log.Printf("Reduce Task ID: %d, State: %v, FileNames: %v\n", task.Id, task.State, task.FileNames)
    }
}

//
// start a thread that listens for RPCs from worker.go
//
func (c *Coordinator) server() {
	rpc.Register(c)
	rpc.HandleHTTP()
	//l, e := net.Listen("tcp", ":1234")
	sockname := coordinatorSock()
	os.Remove(sockname)
	l, e := net.Listen("unix", sockname)
	if e != nil {
		log.Fatal("listen error:", e)
	}
	go http.Serve(l, nil)
}

//
// main/mrcoordinator.go calls Done() periodically to find out
// if the entire job has finished.
//
func (c *Coordinator) Done() bool {
	ret := false

	// Your code here.
    if c.phase == finish {
		return true
	}
	return ret
}

//
// create a Coordinator.
// main/mrcoordinator.go calls this function.
// nReduce is the number of reduce tasks to use.
//
// func MakeCoordinator(files []string, nReduce int) *Coordinator {
// 	c := Coordinator{
		
// 	}

// 	// Your code here.


// 	c.server()
// 	return &c
// }
func MakeCoordinator(files []string, nReduce int) *Coordinator {
	c := Coordinator{
		nMap: len(files),
		nReduce: nReduce,
		phase: duringMap,
		finishedMap: 0,
		finishedReduce: 0,
	}

	for i, file := range files {
		//  the number of map tasks is identical to the number of files
		c.mapTasks = append(c.mapTasks, &Task{
			Type: MapTask,
			FileNames: []string{file},
			Id: i,
			State: Idle,
			NReduce: nReduce,
		})
	}

	c.server()
	return &c
}
