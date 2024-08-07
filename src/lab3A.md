# lab3A

在上一个实验中，我们完成了raft模块的编写，在这个实验中我们将会搭建一个可容错的基于raft共识模块的kv存储引擎。这个kv存储引擎将是一个可复制的状态机，由很多个部署了raft模块的服务器来组成。kv数据服务应该在能够在大多数服务器正常运行的前提下，应对网络分区或者服务器宕机的情况，继续处理来自客户端的请求。
这个kv服务支持三个操作：Put(key, value), Append(key, arg), Get(key)。 服务器提供一个简单的kv数据库，数据库的索引和内容都是string类型的变量。Put() 方法在数据库中替换某个键的值，Append(key, arg) 方法将 arg 追加到键的值之后，而 Get() 方法获取键当前对应的值。对一个不存在的键执行 Get 操作应该返回一个空字符串。对一个不存在的键执行 Append 操作则应该像执行 Put 操作一样，直接设置这个键的值。每个客户端通过一个 Clerk 与服务进行通信，Clerk 提供 Put/Append/Get 方法。Clerk 负责管理与服务器之间的 RPC 交互。
kv服务器必须提供强一致性的服务。这里我们给出强一致性的定义：对外界而言，整个系统就表现地好像只有一台机器在与客户端互动。对于并发调用，返回值和最终状态必须与这些操作按某个顺序逐一执行的结果相同。如果两个调用在时间上有重叠，例如客户端 X 调用了 Clerk.Put()，然后客户端 Y 调用了 Clerk.Append()，随后客户端 X 的调用返回，那么这两个调用就是并发的。此外，某个调用必须能够观察到所有在该调用开始前已经完成的调用所产生的效果（因此技术上要求的是线性一致性）。
根据题目描述，每一个raft server都有一个kvserver与之对应。clerk的任务就是发送rpc给这些kvserver,kvserver再把这些命令提交给raft，这样raft日志中就有了一系列的put/append/get的命令。所有的kvserver按顺序执行来自raft的日志命令，将命令执行到自己的kv数据库中，最主要的目的就是让所有的Kvserver都保留同样状态的kv数据库。
客户端clerk有时会不清楚谁是raft的leader。如果clerk发给follower命令，clerk将重新传给下一个kvserver，也就是要轮询发送rpc。如果kv服务将这个日志提交给raft日志，leader就要把执行的结果传给客户端clerk。如果这条操作没能提交，服务器端就要给客户端clerk报错，clerk将重新找个kvserver发送rpc。kvserver之间只能通过raft共识模块来交流
具体到我们的代码上，我们需要实现一个客户端和一个服务端。客户端中我们需要实现一个clerk实例，这里我们需要细细读一下kvraft/client.go这个文件。
```go
// clerk结构体就是我们的客户端，clerk结构体主要负责和kvserver通信，客户端存放着所有kvserver的地址，方便我们遍历轮询
type Clerk struct {
    servers []*labrpc.ClientEnd
    // You will have to modify this struct.
}
// 这个函数用来生成一个随机的64位整数，大概是用来生成requestID这类型的东西，之后再细看一下
func nrand() int64 {
    max := big.NewInt(int64(1) << 62)
    bigx, _ := rand.Int(rand.Reader, max)
    x := bigx.Int64()
    return x
}
// 创建一个clerk实例，工厂函数
func MakeClerk(servers []*labrpc.ClientEnd) *Clerk {
    ck := new(Clerk)
    ck.servers = servers
    // You'll have to add code here.
    return ck
}
// Get方法应该是从kvserver里面获取某个key对应的value,我猜这里要发送一个rpc，然后取得reply里面的值
func (ck *Clerk) Get(key string) string {
    // You will have to modify this function.
    return ""
}
// Put方法，我觉得应该是在Kvserver里面put一对键值对，调用PutAppend方法
func (ck *Clerk) Put(key string, value string) {
    ck.PutAppend(key, value, "Put")
}
// Append方法，我觉得应该是在Kvserver里面put一对键值对，调用PutAppend方法
func (ck *Clerk) Append(key string, value string) {
    ck.PutAppend(key, value, "Append")
}
// Put和Append方法共同调用的函数。我觉得这里这样处理是因为Put和Append这两个操作很有共性，但具体实现细节还需要再看看
func (ck *Clerk) PutAppend(key string, value string, op string) {
    // You will have to modify this function.
}
```
再来看看common.go文件
```go
// 这三个常量应该是kvserver返回给clerk的状态值，用来表示kvserver对clerk请求是否正常
const (
    OK             = "OK"
    ErrNoKey       = "ErrNoKey"
    ErrWrongLeader = "ErrWrongLeader"
)
// Put和Append共用一个rpc，之后还需要自己在这个rpc里面加一些其他的定义，我怀疑这里需要加随机出来的64位整数。因为它要实现线性一致性，所以这里需要操作一下，待续
type PutAppendArgs struct {
    Key   string
    Value string
    Op    string // "Put" or "Append"
    // You'll have to add definitions here.
    // Field names must start with capital letters,
    // otherwise RPC will break.
}
// 对应的reply
type PutAppendReply struct {
    Err Err
}
// Get函数的rpc，同理，这里也需要保证线性一致性
type GetArgs struct {
    Key string
    // You'll have to add definitions here.
}
// 对应的reply
type GetReply struct {
    Err   Err
    Value string
}
```
写到这里我越来越觉得这个很像uw cse452的lab1，也是实现一个简易的kv服务的客户端服务器端，只不过那里我们是通过为每个command添加一个序列号来保证它的线性一致性，而这里涉及多线程，可能要用随机数来做。继续来看客户端代码
```go
// 我觉得这里应该是要用op命令把之前的命令包装一下
type Op struct {
	// Your definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
}
// KVServer实例
type KVServer struct {
	mu      sync.Mutex
	me      int
    // 每一个kvserver对应一个raft实例
	rf      *raft.Raft
    // kvserver通过raft对应的applyChannel去应用日志命令
	applyCh chan raft.ApplyMsg
	dead    int32 // set by Kill()

    // 这里应该是用来判断是否需要Snapshot的阈值
	maxraftstate int // snapshot if log grows this big

	// Your definitions here.
}
// 由raft的applyChannel传过来的命令，通过调用这两个方法来应用到kv引擎上
func (kv *KVServer) Get(args *GetArgs, reply *GetReply) {
	// Your code here.
}

func (kv *KVServer) PutAppend(args *PutAppendArgs, reply *PutAppendReply) {
	// Your code here.
}
```

首先我们还是来看一下客户端clerk，clerk通过rpc来和kvserver交流，Get方法中，clerk应该构建一对GetArgs和GetReply，那么我们这里来尝试写一下
客户端里要存一个leader的id方便交互。在发送命令的时候，先向leader发送命令，如果发现不是leader或者命令超时，就换个kvserver发送命令，直到得到回复为止。注意，这里的“得到回复”并不意味着kvserver一定有这个get的值，回复成功仅仅意味着kvserver作出了回应，无法保证get是否成功
```go
func (ck *Clerk) Get(key string) string {

	// You will have to modify this function.
	// construct the args and reply
	GetArgs := GetArgs{Key: key}
	for {
		var reply GetReply
        // 模仿给出的例子，调用KVServer的handle get请求的函数
		ok := ck.servers[ck.leaderId].Call("KVServer.Get", &GetArgs, &reply)
        // 经过handle函数，我们要查看是否调用成功，以及回复的内容
		if !ok || reply.Err == ErrWrongLeader || reply.Err == ErrTimeout {
            // 必须在指定的时间内，指定的leader做出成功的调用回复，才能进行下一步，否则重试
			ck.leaderId = (ck.leaderId + 1) % len(ck.servers)
			continue
		}
		return reply.Value
	}
}

// PutAppend和Get差不多
func (ck *Clerk) PutAppend(key string, value string, op string) {
	// You will have to modify this function.
	args := PutAppendArgs{
		Key: key, 
		Value: value, 
		Op: op,
	}
	for {
		var reply PutAppendReply
		ok := ck.servers[ck.leaderId].Call("KVServer.PutAppend", &args, &reply)
		if !ok || reply.Err == ErrWrongLeader || reply.Err == ErrTimeout {
			ck.leaderId = (ck.leaderId + 1) % len(ck.servers)
			continue
		}
		return
	}
}
```
对应地，我们KVServer端也要完成handle函数。kvserver如何处理来自client的请求呢？在lab2中，我们知道我们可以通过Start方法来向leader的raft层提交日志，这里kvserver在收到来自client的请求的时候，调用raft层的Start函数来向leader提交日志，leader收到日志以后，更新自己的commitIndex，将日志通过applyChan这个通道发过去， kvserver收到apply消息后，将命令应用到状态机里，再发送回给client。也就是说，kvserver在收到来自客户端的命令，必须通过raft层的处理才能应用到自己的状态机里面，接着再返回到client里面。
对于client来说，Get和PutAppend方法只需要提供对应的key和value就好，但是对于我们kvserver向raft提交日志来说，我们需要将这些命令包装成Operation，题目中已经为我们定义好了这个结构，我们这里将其补全
```go
type Op struct {
	// Your definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
	Key  string
	Value string
	OpType OperationType
}

type OperationType int

const (
	OpGet OperationType = iota
	OpPut
	OpAppend
)
```
kvserver怎么handle来自client的请求的呢？client直接通过rpc调用kvserver的Get函数和PutAppend函数，然后kvserver通过Start函数调用raft模块，通过channel等待raft的回答。得到回答以后，再回复client,具体如下
```go
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
}

func (kv *KVServer) PutAppend(args *PutAppendArgs, reply *PutAppendReply) {
	// Your code here.
	index, _, isLeader := kv.rf.Start(Op{
		Key: args.Key, 
		Value: args.Value, 
		OpType: getOperationType(args.Op),
	})

	// if not leader, let the client retry
	if !isLeader {
		reply.Err = ErrWrongLeader
		return
	}

	// wait for the result // TODO
}
```
kvserver本身需要维护一个kv状态机，这里的部分实际上和uw cse452的lab1特别像，我记得都是要用到hashmap。所以我们直接定义一个非常简单的state_machine文件：
```go
// state_machine.go
package kvraft

type MemoryKVStateMachine struct {
	KV map[string]string
}

func NewMemoryKVStateMachine() *MemoryKVStateMachine {
	return &MemoryKVStateMachine{
		KV: make(map[string]string),
	}
}

func (mkv *MemoryKVStateMachine) Get(key string) (string, Err) {
	if val, ok := mkv.KV[key]; ok {
		return val, OK
	}
	return "", ErrNoKey
}

func (mkv *MemoryKVStateMachine) Put(key, value string) Err {
	mkv.KV[key] = value
	return OK
}

func (mkv *MemoryKVStateMachine) Append(key, value string) Err {
	mkv.KV[key] += value
	return OK
}
```
别忘了在kvserver结构体中添加这个字段，并进行初始化
kvserver在被client调用get/putAppend方法时，要先调用raft Start函数来发送这个命令，再等待结果。这里有一个小问题，kvserver可能同时要处理很多个操作，每次被调用的时候不能无限制地等待，所以我们要设置一个超时时间。同时，kvserver怎么来等这个消息呢？针对每条命令，kvserver都启动一个channel来听取来自raft层的消息。leader commit这条日志以后就会发送到apply Channel里面，这时候我们kvserver就听到消息，向本机的state_machine同步消息。
在前几节lab中我们提到过，应用层（也就是kvserver）是通过apply channel来接受来自Raft层的applicationTicker的应用命令的，也就是说，我们的kvserver需要监听来自raft的application的消息，具体到上面两个函数就是，等待已久的raft层终于回我们的消息，这里需要在启动每一个kvserver实例的时候就启动一个监听频道，具体如下：
```go
// process the message from raft layer in the applyCh channel
func (kv *KVServer) applyTask() {
	for !kv.killed() {
		select {
        // 一旦收到来自raft层的消息，就开始处理
		case message := <-kv.applyCh:
			if message.CommandValid {
				kv.mu.Lock()
				// if this command has been applied before, skip it
                // 检查这个命令是否已经被应用到我们的kv state_machine里面过
				if message.CommandIndex <= kv.lastApplied {
					kv.mu.Unlock()
					continue
				}

				kv.lastApplied = message.CommandIndex

				// retrieve the command from the message
				op := message.Command.(Op)
				// apply the command to the kv state machine
                // 应用到我们状态机里面，得到结果
				opReply := kv.applyToStateMachine(op)
                // 把结果通过channel发送到这条命令对应的channel中
				// when we get the result, we pass it to the channel
                if _, isLeader := kv.rf.GetState(); isLeader {
					notifyCh := kv.getNotifyChannel(message.CommandIndex)
					notifyCh <- opReply
				}
				kv.mu.Unlock()

			} 
		}
	}
}
```
可以看到，每一个kvserver都有一个永不停歇的监听raft层的channel，kvserver在收到这条消息的时候，应用到自己的kv状态机里面，然后把结果传给自己专门负责处理这条命令的channel里面，这个channel接收到结果以后，再转发给client。

这里，我们还没有实现“线性一致性”的要求，也就是每个客户端的请求应该被执行一次且只执行一次。其实这个实现的方式也比较简单，论文中也提到了，为每一个命令赋予一个独特的标识符，然后再将执行结果存在Map里面，这个部分和cse452 lab1很像。为什么要存在map里面呢？如果我们这个kvserver连续处理两个相同的任务，我们只需要从map里面get一下结果然后输出就可以了。这个独特的标识符，我们可以通过ClientID和seqID的结合体来确定。
```go
duplicateTable map[int64]LastOperationInfo
```
添加了clientID和seqID之后，3A的测试就跑通了，我们注意要把replicationTime设为30毫秒