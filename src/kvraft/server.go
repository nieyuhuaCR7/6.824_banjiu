package kvraft

import (
	"time"

	"6.5840/labgob"
	"6.5840/labrpc"
	"6.5840/raft"

	// "log"
	"sync"
	"sync/atomic"
)


type KVServer struct {
	mu      sync.Mutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg
	dead    int32 // set by Kill()

	maxraftstate int // snapshot if log grows this big

	// Your definitions here.
	stateMachine *MemoryKVStateMachine
	lastApplied int
	notifyChans map[int]chan *OpReply
	duplicateTable map[int64]LastOperationInfo
}

// this function is used when a client sends a Get request to the server
func (kv *KVServer) Get(args *GetArgs, reply *GetReply) {
	// Your code here.
	// use kv.rf.Start() to start a new Raft agreement
	index, _, isLeader := kv.rf.Start(Op{Key: args.Key, OpType: OpGet})
	// if not leader, let the client retry
	if !isLeader {
		reply.Err = ErrWrongLeader
		return
	}

	// wait for the result // TODO
	kv.mu.Lock()
	notifyCh := kv.getNotifyChannel(index)
	kv.mu.Unlock()

	select {
	case result := <-notifyCh:
		reply.Value = result.Value
		reply.Err = result.Err
	case <-time.After(ClientRequestTimeout):
		reply.Err = ErrTimeout
	}

	go func ()  {
		kv.mu.Lock()
		kv.removeNotifyChannel(index)
		kv.mu.Unlock()
	}()
}

func (kv *KVServer) requestDuplicate(clientId int64, seqId int64) bool {
	lastOp, ok := kv.duplicateTable[clientId]
	return ok && seqId <= lastOp.SeqId
}

func (kv *KVServer) PutAppend(args *PutAppendArgs, reply *PutAppendReply) {
	// Your code here.
	// before we process this command, we have to tell if we have processed it before
	kv.mu.Lock()
	if kv.requestDuplicate(args.ClientId, args.SeqId) {
		// if this request is duplicate, we can return the result directly
		opReply := kv.duplicateTable[args.ClientId].Reply
		reply.Err = opReply.Err
		kv.mu.Unlock()
		return
	}
	kv.mu.Unlock()
	index, _, isLeader := kv.rf.Start(Op{
		Key: args.Key, 
		Value: args.Value, 
		OpType: getOperationType(args.Op),
		ClientId: args.ClientId,
		SeqId: args.SeqId,
	})

	// if not leader, let the client retry
	if !isLeader {
		reply.Err = ErrWrongLeader
		return
	}

	// wait for the result
	kv.mu.Lock()
	notifyCh := kv.getNotifyChannel(index)
	kv.mu.Unlock()

	select {
	case result := <-notifyCh:
		reply.Err = result.Err
	case <-time.After(ClientRequestTimeout):
		reply.Err = ErrTimeout
	}

	go func ()  {
		kv.mu.Lock()
		kv.removeNotifyChannel(index)
		kv.mu.Unlock()
	}()
}

// the tester calls Kill() when a KVServer instance won't
// be needed again. for your convenience, we supply
// code to set rf.dead (without needing a lock),
// and a killed() method to test rf.dead in
// long-running loops. you can also add your own
// code to Kill(). you're not required to do anything
// about this, but it may be convenient (for example)
// to suppress debug output from a Kill()ed instance.
func (kv *KVServer) Kill() {
	atomic.StoreInt32(&kv.dead, 1)
	kv.rf.Kill()
	// Your code here, if desired.
}

func (kv *KVServer) killed() bool {
	z := atomic.LoadInt32(&kv.dead)
	return z == 1
}

// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant key/value service.
// me is the index of the current server in servers[].
// the k/v server should store snapshots through the underlying Raft
// implementation, which should call persister.SaveStateAndSnapshot() to
// atomically save the Raft state along with the snapshot.
// the k/v server should snapshot when Raft's saved state exceeds maxraftstate bytes,
// in order to allow Raft to garbage-collect its log. if maxraftstate is -1,
// you don't need to snapshot.
// StartKVServer() must return quickly, so it should start goroutines
// for any long-running work.
func StartKVServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister, maxraftstate int) *KVServer {
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(Op{})

	kv := new(KVServer)
	kv.me = me
	kv.maxraftstate = maxraftstate

	// You may need initialization code here.

	kv.applyCh = make(chan raft.ApplyMsg)
	kv.rf = raft.Make(servers, me, persister, kv.applyCh)

	// You may need initialization code here.
	kv.lastApplied = 0
	kv.dead = 0
	kv.stateMachine = NewMemoryKVStateMachine()
	kv.notifyChans = make(map[int]chan *OpReply)
	kv.duplicateTable = make(map[int64]LastOperationInfo)

	go kv.applyTask()
	return kv
}

// process the message from raft layer in the applyCh channel
func (kv *KVServer) applyTask() {
	for !kv.killed() {
		select {
		case message := <-kv.applyCh:
			if message.CommandValid {
				kv.mu.Lock()
				// if this command has been applied before, skip it
				if message.CommandIndex <= kv.lastApplied {
					kv.mu.Unlock()
					continue
				}

				kv.lastApplied = message.CommandIndex

				// retrieve the command from the message
				op := message.Command.(Op)
				var opReply *OpReply
				if op.OpType != OpGet && kv.requestDuplicate(op.ClientId, op.SeqId) {
					opReply = kv.duplicateTable[op.ClientId].Reply
				} else {
					// apply the command to the kv state machine
					opReply = kv.applyToStateMachine(op)
					if op.OpType != OpGet {
						kv.duplicateTable[op.ClientId] = LastOperationInfo{
							SeqId: op.SeqId,
							Reply: opReply,
						}
					}
				}
				// opReply = kv.applyToStateMachine(op)

				// when we get the result, we pass it to the client
                if _, isLeader := kv.rf.GetState(); isLeader {
					notifyCh := kv.getNotifyChannel(message.CommandIndex)
					notifyCh <- opReply
				}
				kv.mu.Unlock()

			} 
		}
	}
}

func (kv *KVServer) applyToStateMachine(op Op) *OpReply {
	var value string
	var err Err
	switch op.OpType {
	case OpGet:
		value, err = kv.stateMachine.Get(op.Key)
	case OpPut:
		err = kv.stateMachine.Put(op.Key, op.Value)
	case OpAppend:
		err = kv.stateMachine.Append(op.Key, op.Value)
	}
	return &OpReply{
		Value: value,
		Err: err,
	}
}

func (kv *KVServer) getNotifyChannel(index int) chan *OpReply {
	if _, ok := kv.notifyChans[index]; !ok {
		kv.notifyChans[index] = make(chan *OpReply, 1)
	}
	return kv.notifyChans[index]
}

func (kv *KVServer) removeNotifyChannel(index int) {
	delete(kv.notifyChans, index)
}