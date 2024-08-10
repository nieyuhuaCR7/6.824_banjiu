package shardctrler

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"6.5840/labgob"
	"6.5840/labrpc"
	"6.5840/raft"
)


type ShardCtrler struct {
	mu      sync.Mutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg
    dead int32 // set by Kill()
	// Your data here.

	configs []Config // indexed by config num

	stateMachine *CtrlerStateMachine
	lastApplied int
	notifyChans map[int]chan *OpReply
	duplicateTable map[int64]LastOperationInfo
}


// type Op struct {
// 	// Your data here.
// }


func (sc *ShardCtrler) Join(args *JoinArgs, reply *JoinReply) {
	// Your code here.
	fmt.Printf("ShardCtrler %d Join: %v\n", sc.me, args)
	var opReply OpReply
	sc.command(Op{
		OpType: OpJoin,
		ClientId: args.ClientId,
		SeqId: args.SeqId,
		Servers: args.Servers,
	}, &opReply)

	reply.Err = opReply.Err
}

func (sc *ShardCtrler) Leave(args *LeaveArgs, reply *LeaveReply) {
	// Your code here.
	var opReply OpReply
	sc.command(Op{
		OpType: OpLeave,
		ClientId: args.ClientId,
		SeqId: args.SeqId,
		GIDs: args.GIDs,
	}, &opReply)

	reply.Err = opReply.Err
}

func (sc *ShardCtrler) Move(args *MoveArgs, reply *MoveReply) {
	// Your code here.
	var opReply OpReply
	sc.command(Op{
		OpType: OpMove,
		ClientId: args.ClientId,
		SeqId: args.SeqId,
		Shard: args.Shard,
		GID: args.GID,
	}, &opReply)

	reply.Err = opReply.Err
}

func (sc *ShardCtrler) Query(args *QueryArgs, reply *QueryReply) {
	// Your code here.
	var opReply OpReply
	sc.command(Op{
		OpType: OpQuery,
		Num: args.Num,
	}, &opReply)

	reply.Config = opReply.ControllerConfig
	reply.Err = opReply.Err
}

func (sc *ShardCtrler) command(args Op, reply *OpReply) {
	// before we process this command, we have to tell if we have processed it before
	fmt.Printf("Command: Received operation %v with ClientId=%d, SeqId=%d\n", args.OpType, args.ClientId, args.SeqId)
	sc.mu.Lock()
	// we only check the duplication for non-query operations
	if args.OpType != OpQuery && sc.requestDuplicated(args.ClientId, args.SeqId) {
		// if this request is duplicate, we can return the result directly
		fmt.Printf("Command: Duplicate request detected for ClientId=%d, SeqId=%d\n", args.ClientId, args.SeqId)
		opReply := sc.duplicateTable[args.ClientId].Reply
		reply.Err = opReply.Err
		sc.mu.Unlock()
		return
	}
	sc.mu.Unlock()

	index, _, isLeader := sc.rf.Start(args)
	if !isLeader {
		// fmt.Printf("Command: Not the leader, cannot process operation %v\n", args.OpType)
		reply.Err = ErrWrongLeader
		return
	}

	// fmt.Printf("Command: Operation %v started with index %d\n", args.OpType, index)

	// fmt.Printf("every thing is ok until here\n")
	sc.mu.Lock()
	notifyCh := sc.getNotifyChannel(index)
	sc.mu.Unlock()

	// fmt.Printf("Command: Waiting for operation %v to complete with index %d\n", args.OpType, index)
	select {
	case result := <-notifyCh:
		reply.ControllerConfig = result.ControllerConfig
		reply.Err = result.Err
		// fmt.Printf("Command: Operation %v completed with index %d, Err=%v\n", args.OpType, index, result.Err)
	case <-time.After(ClientRequestTimeout):
		reply.Err = ErrTimeout
		// fmt.Printf("Command: Operation %v timed out with index %d\n", args.OpType, index)

	}

	// remove the notify channel
	go func() {
		sc.mu.Lock()
		sc.removeNotifyChannel(index)
		sc.mu.Unlock()
	}()
}


// the tester calls Kill() when a ShardCtrler instance won't
// be needed again. you are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance.
func (sc *ShardCtrler) Kill() {
	atomic.StoreInt32(&sc.dead, 1)
	sc.rf.Kill()
	// Your code here, if desired.
}

func (sc *ShardCtrler) killed() bool {
	z := atomic.LoadInt32(&sc.dead)
	return z == 1
}

// needed by shardkv tester
func (sc *ShardCtrler) Raft() *raft.Raft {
	return sc.rf
}

// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant shardctrler service.
// me is the index of the current server in servers[].
func StartServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister) *ShardCtrler {
	sc := new(ShardCtrler)
	sc.me = me

	sc.configs = make([]Config, 1)
	sc.configs[0].Groups = map[int][]string{}

	labgob.Register(Op{})
	sc.applyCh = make(chan raft.ApplyMsg)
	sc.rf = raft.Make(servers, me, persister, sc.applyCh)

	// Your code here.
	sc.lastApplied = 0
	sc.dead = 0
	sc.stateMachine = NewCtrlerStateMachine() 
	sc.notifyChans = make(map[int]chan *OpReply)
	sc.duplicateTable = make(map[int64]LastOperationInfo)

	go sc.applyTask()
	return sc
}

func (sc *ShardCtrler) applyTask() {
	for !sc.killed() {
		select {
		case message := <-sc.applyCh:
			if message.CommandValid {
				sc.mu.Lock()
				fmt.Printf("ShardCtrler %d: Processing command at index %d\n", sc.me, message.CommandIndex)
				// if this message has been processed, just ignore it
				if message.CommandIndex <= sc.lastApplied {
					sc.mu.Unlock()
					continue
				}
				sc.lastApplied = message.CommandIndex

				// retrieve the operation
				op := message.Command.(Op)
				var opReply *OpReply
				if op.OpType != OpQuery && sc.requestDuplicated(op.ClientId, op.SeqId) {
					opReply = sc.duplicateTable[op.ClientId].Reply
				} else {
					// apply the command to the kv state machine
					fmt.Printf("ShardCtrler %d: Applying operation %v\n", sc.me, op.OpType)
					opReply = sc.applyToStateMachine(op)
					if op.OpType != OpQuery {
						fmt.Printf("ShardCtrler %d: Recording operation %v for ClientId=%d and SeqId=%d\n", sc.me, op.OpType, op.ClientId, op.SeqId)
						sc.duplicateTable[op.ClientId] = LastOperationInfo{
							SeqId: op.SeqId,
							Reply: opReply,
						}
					}
				}

				// notify the client
				fmt.Printf("ShardCtrler %d applyTask: notify client\n", sc.me)
				if _, isLeader := sc.rf.GetState(); isLeader {
					notifyCh := sc.getNotifyChannel(message.CommandIndex)
					notifyCh <- opReply
				}

				sc.mu.Unlock()
			}
		}
	}
}

func (sc *ShardCtrler) requestDuplicated(clientId int64, seqId int64) bool {
	info, ok := sc.duplicateTable[clientId]
	return ok && seqId <= info.SeqId
} 

func (sc *ShardCtrler) applyToStateMachine(op Op) *OpReply {
	var cfg Config
	var err Err
	switch op.OpType {
	case OpQuery:
		cfg, err = sc.stateMachine.Query(op.Num)
	case OpJoin:
		fmt.Printf("ShardCtrler StateMachine %d: Joining servers %v\n", sc.me, op.Servers)
		err = sc.stateMachine.Join(op.Servers)
	case OpLeave:
		err = sc.stateMachine.Leave(op.GIDs)
	case OpMove:
		err = sc.stateMachine.Move(op.Shard, op.GID)
	}
	return &OpReply{
		ControllerConfig: cfg,
		Err: err,
	}
}

func (sc *ShardCtrler) getNotifyChannel(index int) chan *OpReply {
	if _, ok := sc.notifyChans[index]; !ok {
		sc.notifyChans[index] = make(chan *OpReply, 1)
	}
	return sc.notifyChans[index]
}

func (sc *ShardCtrler) removeNotifyChannel(index int) {
	delete(sc.notifyChans, index)
}