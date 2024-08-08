# lab3B

在上一个实验中，我们实现了clerk和kvserver之间的rpc调用通信。kvserver在收到来自Client的请求后，通过Start函数来联系raft层，同时为这个请求创建一个channel。raft层通过Channel将确定好的命令返回过来，kvserver有一个监听频道一直在监听来自raft层的消息，一旦得到这个消息，就会往对应的请求的channel传消息。kvserver在得到这个消息以后，在本地的状态机里面执行这个命令，并存起来，返回给clerk。这大概就是3A的全部内容。在3B中，我们需要在kvserver中实现一个可以与raft模块通信的接口，kvserver进行snapshot，将处理好的snapshot以字节数组的形式传送给raft模块，raft接受以后删掉自己的日志。由于在前面我们已经把raft层的日志删除了，因此我们只需要在kvserver这里正确生成snapshot并且传送给raft就可以。生成snapshot的阈值为maxraftstate，我们需要在启动kvserver的时候传入这个阈值，这个阈值是测试框架提供给我们的。题目中提示我们，要用这个阈值和raft的persister.RaftStateSize()作对比。当阈值为-1时，不需要做快照。
这里有几个问题：
什么时候来判断需要进行snapshot？注意到，每一个kvserver都有一个applyTask线程常驻监听raft，因此我们kvserver每处理一条来自raft的消息时，都要检查一下是否进行snapshot，如果是的话，就调用一个函数来把当前kvserver的状态保存成字节数组，然后传过去到raft那里，raft再通过共识模块将字节数组分发给其他kvserver，其他kvserver再解码，将传过来的字节数组变成自己的状态
KVserver宕机的时候怎么恢复状态？kvserver需要readPersist，重置自己的状态

这一节的代码本身没什么难度，写一个makeSnapshot和restoreFromSnapshot就差不多了
```go
func (kv *KVServer) makeSnapshot(lastIncludedIndex int) {
	buf := new(bytes.Buffer)
	encoder := labgob.NewEncoder(buf)
	encoder.Encode(kv.stateMachine)
	encoder.Encode(kv.duplicateTable)
	kv.rf.Snapshot(lastIncludedIndex, buf.Bytes())
}

func (kv *KVServer) restoreFromSnapshot(snapshot []byte) {
	if len(snapshot) == 0 {
		return
	}
	buf := bytes.NewBuffer(snapshot)
	dec := labgob.NewDecoder(buf)
	var stateMachine MemoryKVStateMachine
	var dupTable map[int64]LastOperationInfo
	if dec.Decode(&stateMachine) != nil || dec.Decode(&dupTable) != nil {
		panic("failed to restore state from snapshpt")
	}

	kv.stateMachine = &stateMachine
	kv.duplicateTable = dupTable

}
```
同时，在每次收到raft层的日志后，都要检查一下是否需要进行snapshot

这节的任务本身不难，但我在跑测试的时候意外发现了2D的好几个bug。在网络不稳定的情况下，如果有延迟的AppendEntries RPC作用到follower或者leader上，由于已经进行了snapshot,访问不到对应的日志就会报错，必须多写几个判断条件才行，这个bug着实费了我不少时间，还好最后是通过了测试