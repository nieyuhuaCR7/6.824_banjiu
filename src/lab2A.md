# lab2A
经过了lab1的小试牛刀，我们正式进入lab2。lab2分为三个部分，分别为领导人选举，日志添加和持久化。那么我们还是照例按顺序，将任务分为一个一个小目标，再去分别实现

说来惭愧，笔者在今年1月份的时候选了校内的分布式系统cse452，一门uw计算机系内的神课。但是并没有全部完成课内的全部任务。cse452是用Java写一个类似于chubby的主从复制机制，然后再实现一个muti-paxos，我当时只完成了第一部分和paxos的部分功能，所以想重新用go写一个简化版的raft出来。

## Leader Election
2A这一部分只设计领导人选举，因此我们在给每一个Raft instance定义状态的时候只需要关注很少一部分的变量，之后的变量再根据我们之后要实现的功能逐步增加。这个原则是符合我们软件开发的增量开发的原则的。一开始最好写一个非常简陋，但是可以运行的版本，让他先跑起来。以免陷入繁琐的debug环节

经过了对论文这一部分的初步讲解，我们现在来尝试完成leader election这一环节

首先我们先定义一个 Raft server 的状态角色：

```go
type Role string

const (
	Follower  Role = "Follower"
	Candidate Role = "Candidate"
	Leader    Role = "Leader"
)
```
同时我们在raft节点里面根据论文，定义出我们需要的几个变量
```go
// A Go object implementing a single Raft peer.
type Raft struct {
	mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.
	
	// 2A
	role Role // this is the role for the current raft server
	currentTerm int // latest term server has seen (initialized to 0 on first boot, increases monotonically)
	votedFor int // // candidateId that received vote in current term (or null if none
    electionStart time.Time // time when election started
	electionTimeout time.Duration // timeout for election
}
```
这里role是我们自己定义的，currentTerm 和 votedFor是论文中提到的。我们在这里暂时先不去关注leader election用不到的变量。这里我们还需要定义一个electionTimeout和electionStart。electionStart表示我们这一轮candidate发起竞选的时间戳，electionStart表示这一轮选举需要持续多久

有了自己定义的状态，我们顺理成章地需要定义三个状态转换函数。

首先我们来看becomeFollower这个函数：
根据论文里的图，一共有三种情况，server会变成follower。开始启动的时候，从candidate变回follower的时候，以及从leader变回follower
一个需要关注的点就是，这个函数应该传入一个term。在raft中，server只会听取任期比自己当前要高的命令，所以每次调用这个方法的时候，我们需要去判断当前任期和给定任期哪个更高
```go
func (rf *Raft) becomeFollowerLocked(term int) {
    // 如果当前的任期高于给定的任期，我们对这个变成follower的操作不予理会
    if (rf.currentTerm > term) {
		// already in a newer term
		log.Printf("Can't become follower, Server %d is already in a newer term %d", rf.me, rf.currentTerm)
		return
	}
    // 此时我们可以确认，当前的任期小于等于给定的任期
    // 如果当前的任期小于给定的任期，这个server立刻将自己之前投过的票作废，并更新任期
	if term > rf.currentTerm {
		rf.votedFor = -1
	}
    // 记得将自己的role和任期进行更新
    rf.role = Follower
	rf.currentTerm = term
    // reset election timer
}
```

figure4中提到，一个server只有两种情况会变为candidate： follower的选举时钟超时后自己变成candidate进行竞选，以及candidate选举时钟超时后进行下一轮选举，因此我们定义函数
```go
func (rf *Raft) becomeCandidateLocked() {
    // 如果这个server本身就已经是leader了，我们不需要去竞选，直接返回并报错
	if (rf.role == Leader) {
		// already a leader	
		log.Printf("Can't become candidate, Server %d is already a leader", rf.me)
		return
	}
    // 把server的状态改成候选人，并将自己的任期加一，选自己一票
	rf.role = Candidate
	rf.currentTerm++
	rf.votedFor = rf.me
	// reset election timer
}
```
只有一种情况，server会变成leader，那就是获得多数选票以后，从candidate变成leader，因此我们定义函数
```go
func (rf *Raft) becomeLeaderLocked() {
    // 只有candidate才能变成leader
	if (rf.role != Candidate) {
		// must be a candidate to become a leader	
		log.Printf("Can't become leader, Server %d must be a candidate to be a leader", rf.me)
		return
	}
	rf.role = Leader
	// reset election timer
}
```

我们这三个函数都没有在状态进行改变的时候重置选举时钟，这里注意要加上这个功能。我们将与选举有关的代码都放到raft_election.go这个文件中，以便于方便我们进行逻辑的实现。所谓重置选举时钟，就是将我们当前server的electionStart设置为当前的时间，然后重新生成一个新的electionTimeout。这里有一个问题，我们electionTimeout为什么不选择一个固定的时间段呢？为什么每次要设置一个随机数呢？这里主要是为了保证不同的server之间每次都有不同的发起选举的顺序。如果设置为一个固定的时间段，由于所有节点的选举超时时长是相同的，竞选碰撞之后每个节点都会再次等待相同的超时时间。这会导致节点们在相同的时间再次进入选举状态，从而无法打破僵局，导致系统一直没有一个稳定的领导者。因此我们要定义一个函数：

```go
func (rf *Raft) resetElectionTimer() {
	rf.electionStart = time.Now()
	randRange := int64(electionTimeoutUpperBound - electionTimeoutLowerBound)
	rf.electionTimeout = electionTimeoutLowerBound + time.Duration(rand.Int63()%randRange)
}
```
有了这个函数，我们就可以在server的角色变化的时候重置选举时钟。

值得注意的是，当我们阅读实验框架给定的代码时，有一行代码起到了很重要的作用，仔细看：

```go
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	// Your initialization code here (2A, 2B, 2C).
	// 2A
	rf.role = Follower
	rf.currentTerm = 0
	rf.votedFor = -1

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())

	// start ticker goroutine to start elections
    // 在这里！
	go rf.ticker()


	return rf
}
```

Make函数的主要功能就是创建一个raft server实例，初始化一系列的参数等等。这里有一个“go rf.ticker()”, 这行代码的含义就是，每当创建一个raft server实例的时候，go rf.ticker() 创建并启动一个新的 goroutine 来执行 rf.ticker() 函数。这样，ticker 函数将与主程序并行执行，而不会阻塞主程序的继续运行。这里的ticker函数其实就是启动了一个选举时钟，只要这个server没有宕机，这个选举时钟就会永远持续下去，定时查看是否需要进行选举。为了方便区分，我们这里将ticker重命名为electionTicker并放到raft_election.go文件内，因为这个ticker主要是用来选举的。将不同的代码部分拆分为不同的文件有利于我们写出可维护的代码。

```go
// 这个函数用来判断当前是否已经选举超时
func (rf *Raft) isElectionTimeout() bool {
	return time.Since(rf.electionStart) > rf.electionTimeout
}

func (rf *Raft) electionTicker() {
    // 只要这个server不宕机，这个选举时钟就会一直存在
	for rf.killed() == false {

		// Your code here (2A)
		// Check if a leader election should be started.
        // 需要上锁
		rf.mu.Lock()
		if rf.role != Leader && rf.isElectionTimeout() {
			// If the server is not a leader and the election timeout has passed,
			// the server should start an election.
            // 只要选举超时，本机不是Leader, 我们就会发起选举，成为候选人
			rf.becomeCandidateLocked()
            // 重置选举时钟
			rf.resetElectionTimer()
			// Start the election.
            // 这里我们需要开始选举
			// go rf.startElection()
		}
		rf.mu.Unlock()


		// pause for a random amount of time between 50 and 350
		// milliseconds.
		ms := 50 + (rand.Int63() % 300)
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}
```

electionTicker会检查当前server是否是leader，如果不是leader，而且超时，就会再调用一个startElection的方法进行竞选。竞选所需要的rpc我们也在这里定义一下:
```go
// example RequestVote RPC arguments structure.
// field names must start with capital letters!
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
    // Term 用来表示，发起选举的这个Candidate当前处于哪个任期
	Term int
    // CandidateId 用来表示，发起选举的Candidate是谁
	CandidateId int
}

// example RequestVote RPC reply structure.
// field names must start with capital letters!
type RequestVoteReply struct {
	// Your data here (2A).
    // 每一个server收到来自Candidate的选举请求之后，都要把自己的任期返回过去
	Term int
    // 如果返回true,则说明当前这个机器愿意为发起选举的候选人投票
	VoteGranted bool
}
```
这个rpc是专门用来进行选举的，在2A部分，我们暂时只需要这些变量，具体的内容在论文的figure2中都有。

现在我们有了用于选举的rpc，那么接下来的一个重头戏就是实现startElection这个函数。startElection函数的主要作用就是candidate向所有的peers发送RequestVoteArgs， 调用peer本身的RequestVoteArgs handler，根据得到的结果RequestVote Reply进行统计选票，最后再判断是否变成leader。这里有一点需要注意，由于rpc调用返回所需的时间很不稳定，很有可能reply返回来的时候我们的candidate早就变成其他状态，任期也发生了变化。这种情况下我们是不需要过多的对这个reply进行操作的，因此我们定义一个检查当前状态的函数

```go
// this function is to check the context of the server is lost or not
func (rf *Raft) contextLostLocked(role Role, term int) bool {
	return rf.role != role || rf.currentTerm != term
}
```

对一个candidate来说，最基本的选举流程就是循环一遍所有的服务器，然后用go协程去分别对所有的服务器进行要票
```go
func (rf *Raft) startElection(term int) {
	// Your code here (2A)
	// Send RequestVote RPCs to all other servers.
	// Increment the term.
	// Vote for self.
	// Reset the election timer.
	votes := 0

	rf.mu.Lock()
	defer rf.mu.Unlock()
	// when starting an election, the server should be a candidate and the term should be correct
	if rf.contextLostLocked(Candidate, term) {
		fmt.Printf("Server %d context lost, not starting election\n", rf.me)
		return
	}
	for peer := 0; peer < len(rf.peers); peer++ {
		if peer == rf.me {
			votes++
			continue
		}
		
		// construct the request vote args
		args := RequestVoteArgs{
			Term: rf.currentTerm,
			CandidateId: rf.me,
		}

		// 这个函数还未实现，但是记住一定要用go的协程来做，不能做成线性的
        go askVoteFromPeer(peer, args)	
	}
}
```
对应地，我们需要实现一个askVoteFromPeer函数。这个函数的主要功能就是构建一个空白的reply，调用函数向peer发送requestVote消息，并处理返回值
```go
votes := 0
	askVoteFromPeer := func(peer int, args RequestVoteArgs) {
		// for every peer, construct an empty reply for them to fill
		reply := RequestVoteReply{}
		// send the request vote rpc to the peer
        // 对这个peer服务器发送请求
		ok := rf.sendRequestVote(peer, &args, &reply)

		// handle the reply
		rf.mu.Lock()
		defer rf.mu.Unlock()
		// check the response
		if !ok {
			fmt.Printf("Candidate Server %d failed to get Vote Response from server %d\n", rf.me, peer)
			return
		}
		// response is valid

		// 得到回复后，原来的candidate很有可能已经变到其他环节了
        // Align the term with the server
		if reply.Term > rf.currentTerm {
			rf.becomeFollowerLocked(reply.Term)
			return
		}

		// check the context
		if rf.contextLostLocked(Candidate, term) {
			fmt.Printf("Server %d context lost, not starting election\n", rf.me)
			return
		}

		// count the votes
		if reply.VoteGranted {
			votes++
			if votes > len(rf.peers)/2 {
				rf.becomeLeaderLocked()
				// start the leader heartbeat
				return
		}
   }
```
这个函数中我们调用了sendRequestVote
```go
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}
```
这段代码是原实验框架给我们的，本质上就是candidate以调用peer的requestvote handler方法来进行通信，这里我们也需要写一下RequestVote方法
```go
// this function is for server to handle RequestVote RPC from candidate
func (rf *Raft) ResquestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).
	rf.mu.Lock()
	defer rf.mu.Unlock()

	// check the term
	// by default, the server will reject the vote request
	reply.Term = rf.currentTerm
	reply.VoteGranted = false
	if args.Term < rf.currentTerm {
		reply.VoteGranted = false
		fmt.Printf("Server %d in term %d rejected vote request from Server %d in term %d, because of lower term\n", rf.me, rf.currentTerm, args.CandidateId, args.Term)
		return
	}
	// Now we know that the candidate term is higher or equal to the server term

	// align the term
	if args.Term > rf.currentTerm {
		rf.becomeFollowerLocked(args.Term)
	}

    // check if the server has already voted
	if rf.votedFor != -1 && rf.votedFor != args.CandidateId {
		reply.VoteGranted = false
		fmt.Printf("Server %d in term %d rejected vote request from Server %d in term %d, because it has already voted for Server %d\n", rf.me, rf.currentTerm, args.CandidateId, args.Term, rf.votedFor)
		return
	}

	// we can now vote for the candidate
	reply.VoteGranted = true
	rf.votedFor = args.CandidateId
	rf.resetElectionTimerLocked()
	fmt.Printf("Server %d in term %d voted for Server %d in term %d\n", rf.me, rf.currentTerm, args.CandidateId, args.Term)
}
```

在 startElection 函数中获取锁后，调用 go askVoteFromPeer(peer, args) 会启动一个新的 goroutine, 在新的 goroutine 中再次获取锁不会导致直接的死锁。

这样，2A部分就仅需实现心跳逻辑了。同样地，只有leader才能发送心跳消息，只要这个leader仍处于他的任期内，就应该每隔一段时间无限循环发送心跳消息。这里我们也定义一个心跳时钟，我们可以新建一个raft_replication文件。同样地，我们也要在rpc文件中定义一下心跳struct

```go
// AppendEntriesArgs is the struct for AppendEntries RPC arguments
type AppendEntriesArgs struct {
	Term int
	LeaderId int
}

// AppendEntriesReply is the struct for AppendEntries RPC reply
type AppendEntriesReply struct {
	Term int
	Success bool
}
```
replication的区间应该更短一点，这里添加定义
```go
const (
	electionTimeoutLowerBound time.Duration = 250 * time.Millisecond
	electionTimeoutUpperBound time.Duration = 400 * time.Millisecond
	replicationInterval time.Duration = 70 * time.Millisecond
)

func (rf *Raft) replicationTicker() {
	for rf.killed() == false {
        ok := rf.startReplication()
		if !ok {
			break
		}
		time.Sleep(replicationInterval)
	}
}
```

startReplication在这一节相对来说较为简单，直接对每一个peer进行单次心跳，并且处理心跳的结果
```go
func (rf *Raft) startReplication(term int) bool {
	// this is the function to send AppendEntries RPCs to one single peer
	replicateToPeer := func(peer int, args *AppendEntriesArgs) {

	}

	rf.mu.Lock()
	defer rf.mu.Unlock()
	// check context
    // 只有leader才可以发出心跳
	if rf.contextLostLocked(Leader, term) {
		fmt.Printf("Server %d context lost, not a leader, role: %v, term: %d\n", rf.me, rf.role, rf.currentTerm)
		return false
	}
	for peer := 0; peer < len(rf.peers); peer++ {
		if peer == rf.me {
			continue
		}
		// construct the append entries args
		args := &AppendEntriesArgs{
			Term: rf.currentTerm,
			LeaderId: rf.me,
		}
		go replicateToPeer(peer, args)
	}
	return true
}

// 单次心跳
replicateToPeer := func(peer int, args *AppendEntriesArgs) {
		// for every peer, construct an empty reply for them to fill
		reply := &AppendEntriesReply{}
		// send the request vote rpc to the peer
		ok := rf.sendAppendEntries(peer, args, reply)

		// handle the reply
		rf.mu.Lock()
		defer rf.mu.Unlock()
		// check the response
		if !ok {
			fmt.Printf("Server %d failed to send append entries to server %d\n", rf.me, peer)
			return
		}
		// check the term
		if reply.Term > rf.currentTerm {
			fmt.Printf("Server %d term outdated, current term: %d, reply term: %d\n", rf.me, rf.currentTerm, reply.Term)
			rf.becomeFollowerLocked(reply.Term)
			return
		}
	}

// 回调函数在这一节相对简单，只需要考虑任期就行了
func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok
}

// this function is for server to handle AppendEntries RPC from leader
func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	reply.Term = rf.currentTerm
	reply.Success = false

	if args.Term < rf.currentTerm {
		reply.Success = false
		fmt.Printf("Server %d in term %d rejected append entries from Server %d in term %d, because of lower term\n", rf.me, rf.currentTerm, args.LeaderId, args.Term)
		return
	}
	rf.becomeFollowerLocked(args.Term)
	fmt.Printf("Server %d in term %d received append entries from Server %d in term %d\n", rf.me, rf.currentTerm, args.LeaderId, args.Term)
	reply.Success = true
}

```

经过测试，发现有data race。排查之后发现是getstate函数忘记加锁了，补上
```go
// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {

	// var term int
	// var isleader bool
	// Your code here (2A).
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.currentTerm, rf.role == Leader
}
```

```bash
(base) yuhuanie@MacBook-Pro raft % go test -run 2A -race
Test (2A): initial election ...
  ... Passed
warning: term changed even though there were no failures  ... Passed --   3.1  3   18    2500    0
Test (2A): election after network failure ...
  ... Passed --   5.0  3   32    2914    0
Test (2A): multiple elections ...
  ... Passed --   5.5  7  232   20198    0
PASS
ok      6.5840/raft     14.831s
```