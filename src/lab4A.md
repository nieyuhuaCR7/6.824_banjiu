# lab4A

从这一节开始我们要实现一个分片的分布式kv存储系统。这里的分片指的是不同集群中存储着不同键值的内容。比如说，所有以'a'为开头的键可以成为一个切片，以'b'为开头的键可以成为一个切片。切片的主要目的是为了追求更好的性能，每一个复制集群应对读写需求，只需要放在众多切片中的一个切片中，整个集群可以并行处理请求，系统吞吐量也会随着切片数量增加而增加。
我们的切片kv存储主要有两个组件。首先，有一组复制集群，每个复制集群都为一个切片负责。一个复制集群由一些raft服务器组成，用来存放切片。其次，由“切片管理员”来管理哪个复制集群需要负责特定的切片，这个信息被称为配置信息。配置信息随着时间变化，客户端咨询切片管理员，我有一个key,这个Key应该放在哪个集群中， 复制集群咨询切片管理员，自己应该存储哪个切片。整个系统有一个切片管理员。
切片存储系统必须有能力在不同的复制集群中转移切片。一个原因是，有些集群中的数据有可能显著多于其他的集群中的数据，因此切片应该被经常移动以便于平衡负载。另一个原因是，复制集群很可能随时加入或者离开这个系统，新的复制集群可能加入系统以便于提高系统容量，已经上线的复制集群也有可能下线去维修。
这个实验主要的难点就在于重建配置：改变不同复制集群的切片。在一个单独的复制集群中，所有的集群成员都必须就客户端的 Put/Append/Get 请求相对于重新配置（reconfiguration）发生的时间达成一致。举例来说，一个 Put 请求可能会在重新配置发生时到达，而这次重新配置会导致副本组不再负责包含该 Put 请求键的分片。这个集群中所有的复制体都必须就这个Put请求是重新配置之前发生还是重新配置之后发生达成一致。如果重新配置之前发生这个put操作，这个操作应该被记录下来，重新配置之后这个切片的新主人应该能看到这个put的结果；如果在重新配置之后发生这个put操作，这个操作不应该生效，客户端必须在这个切片的新主人上重新尝试这个操作。一种比较推荐的做法就是让每一个复制集群用raft来记录put, append, get以及配置变更的所有操作。我们需要保证在任何时刻，每个分片最多只有一个副本组在处理请求。
重新配置的工作同样需要不同复制集群之间的通信。比如说，在编号为10的配置中，Group g1对切片s1负责，在配置11中， g2可能对s1负责。在配置由10变到11的时候，g1和g2必须使用rpc来将s1的内容从G1移动到G2。

注意，只有rpc可以用来实现客户端和服务器之间的通信。比如，不同Server之间禁止传输go的变量或者文件。
这个实验使用“配置”来描述分片在不同复制集群上的分布。这和raft集群成员的变化不同。
这个实验的总体架构（一个配置服务和一组副本组）遵循了与 Flat Datacenter Storage、BigTable、Spanner、FAWN、Apache HBase、Rosebud、Spinnaker 等系统相似的模式。不过，这些系统在很多细节上与本实验不同，而且通常更加复杂和功能强大。例如，本实验并不涉及 Raft 组中对等节点集合的动态调整；它的数据和查询模型也非常简单；分片的切换过程较慢，并且不允许并发的客户端访问。

PartA主要实现切片管理员，文件夹shardctrler里面就是源代码。Shardctrler 管理着一系列编号的配置。每个配置描述了一组副本组以及将分片分配给这些副本组的情况。每当需要更改这个分配时，Shardctrler 会创建一个包含新分配的配置。键/值客户端和服务器在需要了解当前（或过去）配置时，会联系 Shardctrler。
```go
type ShardCtrler struct {
	mu      sync.Mutex
	me      int
    // 每个ShardCtrler对应一个raft服务器
	rf      *raft.Raft
    // Channel，用来接收来自raft日志应用的消息。每当raft日志条目被应用时，都会通过这个通道传给shardCtrler
	applyCh chan raft.ApplyMsg

	// Your data here.

	configs []Config // indexed by config num
}

// A configuration -- an assignment of shards to groups.
// Please don't change this.
type Config struct {
    // Num: 一个整数，表示配置编号。每当分片与副本组之间的映射发生变化时，都会生成一个新的配置，该编号会递增
	Num    int              // config number
    // Shards: 一个数组，其中每个元素表示一个分片对应的副本组ID (gid)。通过分片ID可以找到相应的副本组。
	Shards [NShards]int     // shard -> gid
    // Groups: 一个映射表（map），将副本组ID (gid) 映射到对应的服务器列表。每个副本组由一个或多个服务器组成。
	Groups map[int][]string // gid -> servers[]
}
```
示例代码中的configs []Config就是所有的配置的集合，Config就是用来描述某一时刻分片和复制集群的映射关系。
在实现中，必须支持 shardctrler/common.go 中描述的 RPC 接口。这些接口包括 Join、Leave、Move 和 Query RPCs。它们的作用如下：

## Join:

用于添加新的副本组（Replica Group）。
当一个新的副本组加入时，Join RPC 会接受一个包含新副本组信息的请求，更新当前配置，将一些分片分配给新加入的副本组。

## Leave:

用于移除现有的副本组。
当一个副本组离开时，Leave RPC 会接受一个包含要移除的副本组ID的请求，更新当前配置，将该副本组管理的分片重新分配给其他现有的副本组。

## Move:

用于在不同的副本组之间移动分片。
Move RPC 会接受一个包含分片ID和目标副本组ID的请求，更新当前配置，将指定的分片分配给指定的副本组。

## Query:

用于查询当前或历史配置。
Query RPC 会接受一个包含配置编号的请求，并返回对应编号的配置。如果请求的配置编号是最新的，则返回当前最新的配置。
这些 RPC 接口允许管理员或测试代码通过控制 shardctrler 来动态地管理系统中分片与副本组的映射关系。通过这些操作，shardctrler 可以灵活地适应副本组的加入、离开以及分片的重新分配，从而保证系统的灵活性和可扩展性。

## ShardCtrler 配置要求

1. **初始配置（配置编号 0）**:
   - 配置编号应为 `0`。
   - 该配置应不包含任何副本组 (`Groups` 字段为空)。
   - 所有分片 (`Shards` 数组中的元素) 应分配给 `GID 0`，这是一个无效的 GID。

2. **后续配置**:
   - 在接收到 `Join` RPC 请求后，`ShardCtrler` 应创建一个新的配置，其编号为 `1`，依此类推。
   - 新配置中的 `Groups` 字段应反映当前活跃的副本组，而 `Shards` 数组中的分片应根据新的副本组进行重新分配。

3. **分片分配**:
   - 通常情况下，系统中分片的数量会显著多于副本组的数量。这样设计是为了在较细粒度的水平上平衡负载。
   - 每个副本组将管理多个分片，以便在副本组之间平衡负载，确保系统高效运行。

通过这些设计，`ShardCtrler` 能够有效管理分片与副本组的映射关系，并能够在副本组数量发生变化时动态调整分片的分配以平衡负载。

以上是lab4A实验的提示和说明，我这里来写一下我自己的想法
我们之前lab23实现的功能，就是一个raft共识模块再加上一个基于raft模块的kv存储引擎。具体到lab4里面，每一个replica group就是一个分布式kv存储引擎，可以说replica group我们已经在之前的实验中完成了。对于lab4的shardKv来说，有好多个shard，每一个shard都对应不同的replica group，每一个replica group都要存储不同的shard数据，比如我们现在有5个replica group，每一个group里面有若干个kvserver, 这些server都存储相同的数据，但是不同group存储的数据是不同的。我们要添加的功能具体是这样的：
1. 实现不同group之间的数据通信，数据传输。
2. 得有一个专门用来管理shardkv group的replica 集群，也就是shardCtrler。shardCtrler具体也用到了raft模块，一群shardctrler server中也有Leader， follower这些角色，他们共同存储了整个shardkv系统的配置信息，共识算法需要对这些配置信息形成共识。从这个角度来看，shardctrler server其实和kvserver挺像的，我们可以复用之前的代码
3. 之前的客户端叫clerk，用来给replica group中的Kvserver提交日志操作，这里clerk还需要和shardctrler server通信，对整个系统的配置指手画脚。

经过上述分析，结构比较清晰了，那么我们现在先按照lab3的方式对clerk进行一些改进
```go
// 模仿lab3，为客户端增添几个变量并初始化
type Clerk struct {
	servers []*labrpc.ClientEnd
	// Your data here.
	leaderId int
	clientId int64 // unique id for each client
	seqId    int64
}
// 记得要初始化
func MakeClerk(servers []*labrpc.ClientEnd) *Clerk {
	ck := new(Clerk)
	ck.servers = servers
	// Your code here.
	ck.leaderId = 0
	ck.clientId = nrand()
	ck.seqId = 0
	return ck
}
```
我们仿照kvserver,对Query函数进行改造
```go
func (ck *Clerk) Query(num int) Config {
	args := &QueryArgs{}
	// Your code here.
	args.Num = num
	for {
		var reply QueryReply
		ok := ck.servers[ck.leaderId].Call("ShardCtrler.Query", args, &reply)
		if !ok || reply.Err == ErrWrongLeader || reply.Err == ErrTimeout {
			ck.leaderId = (ck.leaderId + 1) % len(ck.servers)
			continue
		}
		return reply.Config
	}
}
```
四种rpc，除了Query，剩下的操作都需要附加ClientId和SeqId，否则就丧失了线性一致性。这里给出一个示例代码，基本上都是一样的
```go
func (ck *Clerk) Join(servers map[int][]string) {
	args := &JoinArgs{
		ClientId: ck.clientId,
		SeqId:   ck.seqId,
	}
	// Your code here.
	args.Servers = servers

	for {
		var reply JoinReply
		ok := ck.servers[ck.leaderId].Call("ShardCtrler.Join", args, &reply)
		if !ok || reply.Err == ErrWrongLeader || reply.Err == ErrTimeout {
			ck.leaderId = (ck.leaderId + 1) % len(ck.servers)
			continue
		}
		ck.seqId++
		// print a log for debugging
		fmt.Printf("Join: %v\n", servers)
		return
	}
}
```
在完成了client后，我们来处理server，server和kvraft 里面的Server思路差不多，因此我们直接借鉴之前的思路和代码。shardctrler文件夹中新建一个state_machine.go文件，用来定义我们shardctrler内部的状态机。新的状态机里面的方法应该和之前的get, put, append有所区别，我们暂时先跳过。
还是要来观察一下commons.go这个文件，里面定义了四种rpc的参数和回复。为了简化操作，我们将这四种操作归纳到一个大的抽象Op里面，然后把全部的信息都塞进去，这样不管用到什么rpc，我们只需要发送一个Op，回收一个OpReply就行了
```go
type Op struct {
	// Your definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
	Servers map[int][]string
	GIDs []int
	Shard int
	GID int
	Num int
	OpType OperationType
	ClientId int64
	SeqId int64
}

type OpReply struct {
	ControllerConfig      Config
	Err  Err
}

type OperationType int

const (
	OpJoin OperationType = iota
	OpLeave
	OpMove
	OpQuery
)
```
定义一个command方法，用来包装我们shardctrler和raft的通信，和kvraft的PutAppend方法特别像
```go
func (sc *ShardCtrler) command(args Op, reply *OpReply) {
	// before we process this command, we have to tell if we have processed it before
    // 为了保证线性一致性，在进行操作之前我们需要检查一下之前有没有处理过这个命令
	sc.mu.Lock()
	// we only check the duplication for non-query operations
	if args.OpType != OpQuery && sc.requestDuplicated(args.ClientId, args.SeqId) {
		// if this request is duplicate, we can return the result directly
		opReply := sc.duplicateTable[args.ClientId].Reply
		reply.Err = opReply.Err
		sc.mu.Unlock()
		return
	}
	sc.mu.Unlock()

    // 把这个命令传送给raft层，而且只能是leader接收这个命令
	index, _, isLeader := sc.rf.Start(args)
	if !isLeader {
		reply.Err = ErrWrongLeader
		return
	}
    
    // 等待raft层回消息
	sc.mu.Lock()
	notifyCh := sc.getNotifyChannel(index)
	sc.mu.Unlock()

	select {
	case result := <-notifyCh:
		reply.ControllerConfig = result.ControllerConfig
		reply.Err = result.Err
	case <-time.After(ClientRequestTimeout):
		reply.Err = ErrTimeout
	}

	// remove the notify channel
	go func() {
		sc.mu.Lock()
		sc.removeNotifyChannel(index)
		sc.mu.Unlock()
	}()
}
// 这四个函数长得都大同小异
func (sc *ShardCtrler) Join(args *JoinArgs, reply *JoinReply) {
	// Your code here.
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
```
shardctrler在后台也要时刻运行一个applyTask线程来时刻监听来自raft层的消息，实现思路也差不多，甚至还比kvserver少了snapshot的处理
```go
func (sc *ShardCtrler) applyTask() {
	for !sc.killed() {
		select {
		case message := <-sc.applyCh:
			if message.CommandValid {
				sc.mu.Lock()
				// if this message has been processed, just ignore it
                // 只处理最新的raft的消息
				if message.CommandIndex <= sc.lastApplied {
					sc.mu.Unlock()
					continue
				}
				sc.lastApplied = message.CommandIndex

				// retrieve the operation
				op := message.Command.(Op)
				var opReply *OpReply
                // Query操作相当于get，get不会影响线性一致性
				if op.OpType != OpQuery && sc.requestDuplicated(op.ClientId, op.SeqId) {
					opReply = sc.duplicateTable[op.ClientId].Reply
				} else {
					// apply the command to the kv state machine
					opReply = sc.applyToStateMachine(op)
					if op.OpType != OpQuery {
						sc.duplicateTable[op.ClientId] = LastOperationInfo{
							SeqId: op.SeqId,
							Reply: opReply,
						}
					}
				}

				// notify the client
                // shardctrler在进行command操作的时候会单独为这个操作创建一个channel,我们这个常驻线程要把结果发送给这个channel，channel收到消息后再返回给client,当然，这个操作只有Leader才能完成
				if _, isLeader := sc.rf.GetState(); isLeader {
					notifyCh := sc.getNotifyChannel(message.CommandIndex)
					notifyCh <- opReply
				}

				sc.mu.Unlock()
			}
		}
	}
}
```

回过头来看一下，lab4A的难点在于，如何处理四种rpc。我们不仅仅要增加和减少复制集群，把shard分给不同的集群，我们还要在新增或者减少集群的时候重新平衡整个系统的配置，使其更加合理。因此，lab4中的状态机也要实现四种rpc所要求的功能。与lab3的状态机不同，lab4的状态机是一组Config配置，最新的Config代表最新的配置，每次在收到一个rpc的时候，lab4的状态机就需要根据rpc功能的要求，生成一组新的Config配置，并同步到所有lab4 shardctrler的机器上面。因此我们这里要实现一下shardctrler的状态机
```go
type CtrlerStateMachine struct {
	Configs []Config // indexed by config num
}

func NewCtrlerStateMachine() *CtrlerStateMachine {
	cf := &CtrlerStateMachine{Configs: make([]Config, 1)}
    // 第一组配置是无效的，我们这里随便定义一组配置就行，起到一个dummy节点的作用
	cf.Configs[0] = DefaultConfig()
	return cf
}

// Query rpc起到的作用和get类似，给定一个num，如果超界，就返回最新的配置信息；其他情况正常返回配置
func (csm *CtrlerStateMachine) Query(num int) (Config, Err) {
	if num < 0 || num >= len(csm.Configs) {
		return csm.Configs[len(csm.Configs)-1], OK
	}
	return csm.Configs[num], OK
}
```
lab4A的重头戏就是实现Join这个函数。我们先来看一下lab中对Join函数的描述：Join RPC 是由管理员用来添加新的副本组（replica groups）。它的参数是一个从唯一的非零副本组标识符（GID）到服务器名称列表的映射集合。shardctrler 应该通过创建一个包含新副本组的新配置来做出响应。新的配置应尽可能均匀地将分片分配到所有副本组中，并且在实现这个目标时应尽可能少地移动分片。
我个人的理解是这样的：
举个例子，假设在Join之前，系统中有10个shard，3个group，每个group分配了一些shard, 分配情况可能是这样的：
groupId    shards
1          1, 2, 3, 4
2          5, 6, 7
3          8, 9, 10
具体到Config里面，大概是这样的
shardID   groupID
1         1
2         1
3         1
4         1
5         2
6         2
7         2
8         3
9         3
10        3
这时如果新添加两组groups, gip分别为4， 5，那么应该出现的情况为
groupId    shards
1          1, 2
2          5, 6
3          8, 9
4          4, 7
5          3, 10
具体到Config里面，大概是这样的
shardID   groupID
1         1
2         1
3         5
4         4
5         2
6         2
7         4
8         3
9         3
10        5
排个序，然后重新分配
每个groupID对应一组用String表示的服务器，在此不表。我还注意到，在Config结构体中，一个shard只能对应一个group,这是为了方便每一个Shard的数据更新。如果 group 的数量超过 shard 的数量，这意味着有更多的副本组可用，但 shard 数量不足以分配给每个组
func (csm *CtrlerStateMachine) Join(groups map[int][]string)，参数groups是个map, map的keySet就是新增的gid。在最近的配置的基础上，添加新的groups，然后重新分配，就起到了Join的作用。新传进来的gid很有可能在之前的配置中已经存在了，但是join操作只会为之前不存在的gid提供新的服务器，现有的gid和对应的服务器列表不会被修改。
```go           
// add new groups into the system, may need to rebalance the shards
func (csm *CtrlerStateMachine) Join(groups map[int][]string) Err {
	num := len(csm.Configs)
	lastConfig := csm.Configs[num-1]
	// construct a new config
    // 取出最新的配置，复制一份
	newConfig := Config{
		Num:    num,
		Shards: lastConfig.Shards,
		Groups: copyGroups(lastConfig.Groups),
	}

	// add new groups into the new config
    // 把join操作传进来的新的服务器都添加进来，只处理没有在配置中出现的Gid
	for gid, servers := range groups {
		if _, ok := newConfig.Groups[gid]; !ok {
			newServers := make([]string, len(servers))
			copy(newServers, servers)
			newConfig.Groups[gid] = newServers
		}
	}

	// construct a mapping from gid to shards
    // 就像我们上面提到的一样，创建一个新的映射
	gidToShards := make(map[int][]int)
	for gid := range newConfig.Groups {
		gidToShards[gid] = make([]int, 0)
	}
	for shard, gid := range newConfig.Shards {
		gidToShards[gid] = append(gidToShards[gid], shard)
	}
  
	// rebalance the shards
	for {
        // 找到最多shards的Gid和最少shards的gid
		maxGid := gidWithMaxShards(gidToShards)
		minGid := gidWithMinShards(gidToShards)
        // 如果差值小于等于1，才跳出去
		if maxGid != 0 && len(gidToShards[maxGid]) - len(gidToShards[minGid]) <= 1 {
			break
		}

		// move a shard from maxGid to minGid
		gidToShards[minGid] = append(gidToShards[minGid], gidToShards[maxGid][0])
		gidToShards[maxGid] = gidToShards[maxGid][1:]
	}

    // now we got the new shard allocation, update the new config
    // 把格式变回去
	var newShards [NShards]int
	for gid, shards := range gidToShards {
		for _, shard := range shards {
			newShards[shard] = gid
		}
	}
	newConfig.Shards = newShards
    // 把新建好的配置加到配置项里面
	csm.Configs = append(csm.Configs, newConfig)

	return OK
}

func gidWithMaxShards(gidToShards map[int][]int) int {
	if shard, ok := gidToShards[0]; ok && len(shard) > 0 {
		return 0
	}
	
	var gids []int
	for gid := range gidToShards {
		gids = append(gids, gid)
	}
	sort.Ints(gids)

	maxGid, maxShards := -1, -1
	for _, gid := range gids {
		if len(gidToShards[gid]) > maxShards {
			maxGid = gid
			maxShards = len(gidToShards[gid])
		}
	}
	return maxGid
}
```
在完成了join函数后，我们来看leave函数。leave函数给定一些group id，要求我们在状态机中删掉这些group。这个函数相对简单，只要遍历一遍group id，如果这个group id在配置中存在，就删掉，并且将这个Group对应的shard记录下来。遍历完以后，再分配给剩下的group。
```go
func (csm *CtrlerStateMachine) Leave(gids []int) Err {
	num := len(csm.Configs)
	lastConfig := csm.Configs[num-1]
	// construct a new config
	newConfig := Config{
		Num:    num,
		Shards: lastConfig.Shards,
		Groups: copyGroups(lastConfig.Groups),
	}

	// construct a mapping from gid to shards
    // 得到一个group id到切片的映射
	gidToShards := make(map[int][]int)
	for gid := range newConfig.Groups {
		gidToShards[gid] = make([]int, 0)
	}
	for shard, gid := range newConfig.Shards {
		gidToShards[gid] = append(gidToShards[gid], shard)
	}

	// delete the given groups and store the shards
    // 遍历一遍要删的group，如果存在，就删掉，并且记录下来对应的shard
	var unassignedShards []int
	for _, gid := range gids {
		// if this gid is in the new config, delete it
		if _, ok := newConfig.Groups[gid]; ok {
			delete(newConfig.Groups, gid)
		}
		// retrieve the shards of this gid
		if shards, ok := gidToShards[gid]; ok {
			unassignedShards = append(unassignedShards, shards...)
			delete(gidToShards, gid)
		}
	}

	var newShards [NShards]int
	// redistribute the unassigned shards
    // 重新分配shard
	if len(unassignedShards) > 0 {
		sort.Ints(unassignedShards)
		for _, shard := range unassignedShards {
			minGid := gidWithMinShards(gidToShards)
			gidToShards[minGid] = append(gidToShards[minGid], shard)
		}
	
		// now we got the new shard allocation, update the new config
		for gid, shards := range gidToShards {
			for _, shard := range shards {
				newShards[shard] = gid
			}
		}
	}

	newConfig.Shards = newShards
	csm.Configs = append(csm.Configs, newConfig)
    return OK
}
```
Move函数就更简单了，参数为一个shard和一个gid，只需要将这个Shard的gid设为对应的gid就行了
```go
func (csm *CtrlerStateMachine) Move(shard int, gid int) Err {
	num := len(csm.Configs)
	lastConfig := csm.Configs[num-1]
	// construct a new config
	newConfig := Config{
		Num:    num,
		Shards: lastConfig.Shards,
		Groups: copyGroups(lastConfig.Groups),
	}

	// update the shard allocation
	newConfig.Shards[shard] = gid
	csm.Configs = append(csm.Configs, newConfig)
	return OK
}
```