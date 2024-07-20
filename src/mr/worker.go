package mr

import (
	"bufio"
	"fmt"
	"hash/fnv"
	"io/ioutil"
	"log"
	"net/rpc"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//
// Map functions return a slice of KeyValue.
//
type KeyValue struct {
	Key   string
	Value string
}

//
// use ihash(key) % NReduce to choose the reduce
// task number for each KeyValue emitted by Map.
//
func ihash(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() & 0x7fffffff)
}


//
// main/mrworker.go calls this function.
//
func Worker(mapf func(string, string) []KeyValue, reducef func(string, []string) string) {

	// Your worker implementation here.

	// uncomment to send the Example RPC to the coordinator.
	// CallExample()
	// CallGetTask(mapf)
	timeout := 10 * time.Second  // setting up timeout
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	
	// use a for loop to repeatedly process all of the tasks
	for {
		select {
		case <-timer.C:
			// fmt.Println("Worker: No tasks received for a while, exiting.")
			return
		default:
			task := CallGetTask()
			if task == nil {
				fmt.Println("task is nil, continue")
				time.Sleep(2 * time.Second)
				continue
			}

			// fmt.Printf("worker: receive coordinators get task: %v\n", task)
			switch task.Type {
			case MapTask:
				HandleMapTask(task, mapf)
			case ReduceTask:
				HandleReduceTask(task, reducef)
			case AllTasksDone:
				// all tasks are done, the worker should return
				fmt.Printf("all tasks are done, worker exit")
				return
			default:
				fmt.Printf("unexpected TaskType %v\n", task.Type)
				time.Sleep(time.Second)
				timer.Reset(timeout)  
				continue
			}

			timer.Reset(timeout)  
		}
	}

}

//
// example function to show how to make an RPC call to the coordinator.
//
// the RPC argument and reply types are defined in rpc.go.
//
func CallExample() {

	// declare an argument structure.
	args := ExampleArgs{}

	// fill in the argument(s).
	args.X = 99

	// declare a reply structure.
	reply := ExampleReply{}

	// send the RPC request, wait for the reply.
	// the "Coordinator.Example" tells the
	// receiving server that we'd like to call
	// the Example() method of struct Coordinator.
	ok := call("Coordinator.Example", &args, &reply)
	if ok {
		// reply.Y should be 100.
		fmt.Printf("reply.Y %v\n", reply.Y)
	} else {
		fmt.Printf("call failed!\n")
	}
}

// this function is only used to get the task from coordinator via rpc
func CallGetTask() *Task{

	// declare an argument structure.
	args := GetTaskRequest{}

	// fill in the argument(s).
	args.Worker = 99

	// declare a reply structure.
	reply := GetTaskResponse{}

	// send the RPC request, wait for the reply.
	// the "Coordinator.HandleGetTaskRequest" tells the
	// receiving server that we'd like to call
	// the HandleGetTaskRequest method of struct Coordinator.
	ok := call("Coordinator.HandleGetTaskRequest", &args, &reply)
	if ok {
		// log.Printf("Worker: Received task: %+v\n", reply.Task)
		if reply.Task == nil {
			// No more tasks
			return nil
		}
		// log.Printf("Worker: Received task: %+v\n", reply.Task)
		return reply.Task
	} else {
		fmt.Printf("CallGetTask failed!\n")
		return nil
	}
}

func notifyTaskDone(task *Task) {
	args := TaskDoneOrNotRequest{Task: task}
	reply := TaskDoneOrNotReply{}

	ok := call("Coordinator.TaskDone", &args, &reply)
	if ok {
		// fmt.Printf("Worker: Notified task done: %+v\n", task)
	} else {
		fmt.Printf("Worker: Failed to notify task done: %+v\n", task)
	}
}

func HandleMapTask(task *Task, mapf func(string, string) []KeyValue) {

	// defer func() {
	// 	task.State = Completed
	// 	if err := recover(); err != nil {
	// 		// panic 前执行收尾工作
	// 		fmt.Printf("worker: doMap panic, %s", err)
	// 		task.State = Idle // 通知 Coordinator 本次任务执行失败
	// 	}
	// 	// callTaskDoneOrNot(task)

	

	defer func() {
		if err := recover(); err != nil {
			fmt.Printf("worker: doMap panic, %s", err)
			task.State = Idle // 通知 Coordinator 本次任务执行失败
		} else {
			task.State = Completed
		}
		notifyTaskDone(task)

		// 删除可能存在的中间文件
		for i := 0; i < task.NReduce; i++ {
			intermediateFile := fmt.Sprintf("./intermediates/mr-%s-%d.tmp%v", strings.TrimSuffix(filepath.Base(task.FileNames[0]), ".txt"), i, task.StartTime)
			os.Remove(intermediateFile)
		}
	}()
	
	// 1. Read input file
	// since it is Map task, we only need to handle one file
	file, err := os.Open(task.FileNames[0])
	if err != nil {
		log.Fatalf("cannot open %v", task.FileNames[0])
	}
	content, err := ioutil.ReadAll(file)
	if err != nil {
		log.Fatalf("cannot read %v", task.FileNames[0])
	}
	file.Close()

	// 2. use our predefined map function to process the file
	kvs := mapf(task.FileNames[0], string(content))

	// 3. write the kv pairs into NReduce files
	// os.MkdirAll("./intermediates/", os.ModeDir) // create intermediate keyValue file directory
	intermediateDir := "./intermediates"
	err = os.MkdirAll(intermediateDir, os.ModePerm)
	if err != nil {
		log.Fatalf("cannot create directory %v", intermediateDir)
	}
	// here we need to split the file into NReduce number of files
	bucketfiles := make([]*os.File, task.NReduce)
	// for i := 0; i < task.NReduce; i++ {
	// 	// name the temp files with orignal filename and other components
	// 	// here we use the start time naming to avoid the situation of files having the same name
	// 	intermediateFile := fmt.Sprintf("./intermediates/mr-%s-%d.tmp%v", 
	// 		strings.TrimSuffix(filepath.Base(task.FileNames[0]), ".txt"), 
	// 		i, task.StartTime.UnixNano())
	// 	f, err := os.OpenFile(intermediateFile, os.O_CREATE|os.O_RDWR|os.O_APPEND, os.ModePerm)
	// 	if err != nil {
	// 		panic(err)
	// 	}
	// 	defer f.Close()
	// 	os.Chmod(intermediateFile, os.ModePerm)
	// 	bucketfiles[i] = f
	// }
	for i := 0; i < task.NReduce; i++ {
		intermediateFile := fmt.Sprintf("%s/mr-%d-%d.tmp%v",
			intermediateDir,
			task.Id,
			i,
			task.StartTime.UnixNano())
		f, err := os.OpenFile(intermediateFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
		if err != nil {
			log.Fatalf("cannot create intermediate file %v", intermediateFile)
		}
		bucketfiles[i] = f
		defer f.Close()
	}

	// write the kv pairs into NReduce files
	for _, kv := range kvs {
		// using ihash function to find out which file the key value pair should be written into
		idx := ihash(kv.Key) % task.NReduce
		_, err := bucketfiles[idx].WriteString(fmt.Sprintf("%s %s\n", kv.Key, kv.Value))
		if err != nil {
			panic(fmt.Sprintf("worker crash on map task, fail append kv (%s, %s) to intermediate file %s", kv.Key, kv.Value, bucketfiles[idx].Name()))
		}
	}
	// Close all files
	for _, file := range bucketfiles {
		file.Close()
	}

	// rename the temp files we just created
	// for _, file := range bucketfiles {
	// 	// trim out the .tmp + starttime suffix
	// 	oname := strings.TrimSuffix(file.Name(), fmt.Sprintf(".tmp%v", task.StartTime.UnixNano()))
	// 	// try to rename the file
	// 	if err := os.Rename(file.Name(), oname); err != nil {
	// 		lastfile := strings.TrimSuffix(bucketfiles[len(bucketfiles)-1].Name(), fmt.Sprintf(".tmp%v", task.StartTime.UnixNano()))
	// 		if _, err := os.Lstat(lastfile); err != nil {
	// 			os.Remove(strings.TrimSuffix(file.Name(), fmt.Sprintf(".tmp%v", task.StartTime.UnixNano())))
	// 			os.Rename(file.Name(), strings.TrimSuffix(file.Name(), fmt.Sprintf(".tmp%v", task.StartTime.UnixNano())))
	// 		}
	// 	}
	// }
	// Rename the temporary files
	for _, file := range bucketfiles {
		originalName := file.Name()
		finalName := strings.TrimSuffix(originalName, filepath.Ext(originalName))
		err := os.Rename(originalName, finalName)
		if err != nil {
			log.Fatalf("cannot rename intermediate file %v to %v", originalName, finalName)
		}
	}
}

func HandleReduceTask(task *Task, reducef func(string, []string) string) {

	defer func() {
		if err := recover(); err != nil {
			fmt.Printf("worker: reduce panic, %s", err)
			task.State = Idle 
		} else {
			task.State = Completed
		}
		notifyTaskDone(task)

		os.Remove(fmt.Sprintf("mr-out-%d.tmp%v", task.Id, task.StartTime))
	}()
	
	var kvs []KeyValue
	for _, fileName := range task.FileNames {
		// first we have to open the file
		f, err := os.Open(fileName)
		// log.Println("fileName is: \n")
		// log.Println(fileName)
		// log.Println("\n")
		if err != nil {
			panic(fmt.Sprintf("worker crash on reduce task, fail to open %s\n", fileName))
		}
		defer f.Close()

		// here we scan through each line of the txt file
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			kvStrs := strings.Fields(sc.Text())
			if len(kvStrs) != 2 {
				panic("worker crash on reduce task, kv format wrong")
			}
			kvs = append(kvs, KeyValue{
				Key:   kvStrs[0],
				Value: kvStrs[1],
			})
		}

		// now we have all of the KeyValues in a slice, sort this slice according to key
		sort.Slice(kvs, func(i, j int) bool {
			return kvs[i].Key < kvs[j].Key
		})

		// create a temporary file
		oname := fmt.Sprintf("mr-out-%d.tmp%v", task.Id, task.StartTime)
		ofile, err := os.Create(oname)
		if err != nil {
			panic(err)
		}
		defer ofile.Close()

		i := 0 // start idx for same keys
		for i < len(kvs) {
			j := i + 1 // end idx for same keys
			for j < len(kvs) && kvs[j].Key == kvs[i].Key {
				j++
			}

			// collect values with the same key
			values := []string{}
			for k := i; k < j; k++ {
				values = append(values, kvs[k].Value)
			}
			output := reducef(kvs[i].Key, values)

			// this is the correct format for each line of Reduce output.
			fmt.Fprintf(ofile, "%v %v\n", kvs[i].Key, output)

			i = j
		}

		// rename the file
		os.Rename(ofile.Name(), strings.TrimSuffix(ofile.Name(), fmt.Sprintf(".tmp%v", task.StartTime)))

	}
}

//
// send an RPC request to the coordinator, wait for the response.
// usually returns true.
// returns false if something goes wrong.
//
func call(rpcname string, args interface{}, reply interface{}) bool {
	// c, err := rpc.DialHTTP("tcp", "127.0.0.1"+":1234")
	sockname := coordinatorSock()
	c, err := rpc.DialHTTP("unix", sockname)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	defer c.Close()

	err = c.Call(rpcname, args, reply)
	if err == nil {
		return true
	}

	fmt.Println(err)
	return false
}
