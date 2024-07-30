# lab2B

完成lab2A的领导者选举功能后，我们来看日志同步

首先，我们需要定义一下什么是日志：
```go
type LogEntry struct {
	Term int // term when the command was received by the leader
	CommandValid bool // whether the command is valid
	Command interface{} // the command
	CommandIndex int // the index of the command
}
```
这里的CommandValid和Command都是我们从raft结构体中借鉴过来的，CommandValid的作用我暂时不清楚，Command在这里起到一个接口的作用，用户可以自定义这个命令。从论文中我们可以看到，每条日志必须有这条日志被创建时leader的任期，以及对应的序列号。

接着我们根据论文的图2来补充AppendEntries RPC的内容, 以及raft结构体的内容
```go
// AppendEntriesArgs is the struct for AppendEntries RPC arguments
type AppendEntriesArgs struct {
	// 2A
	Term int
	LeaderId int
	// 2B
	PrevLogIndex int // index of log entry immediately preceding new ones
	PrevLogTerm int // term of prevLogIndex entry
	Entries []LogEntry // log entries to store (empty for heartbeat; may send more than one for efficiency)
}

// AppendEntriesReply is the struct for AppendEntries RPC reply
type AppendEntriesReply struct {
	Term int
	Success bool
}

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
	role Role // indicate the current role of the server
	currentTerm int // latest term server has seen
	votedFor int // candidateId that received vote in current term
	electionStart time.Time // time when election started
	electionTimeout time.Duration // timeout for election

	// 2B
	log []LogEntry // log entries; each entry contains command for state machine, and term when entry was received by leader
    nextIndex []int // for each server, index of the next log entry to send to that server	
    matchIndex []int // for each server, index of highest log entry known to be replicated on server	
}
```

同样地，在创建每个server的时候，也要对这些变量进行初始化
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
	// 2B
	rf.log = append(rf.log, LogEntry{}) // a dummy entry for each server to avoid corner cases
	rf.nextIndex = make([]int, len(peers))
	rf.matchIndex = make([]int, len(peers))

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())

	// start ticker goroutine to start elections
	go rf.electionTicker()


	return rf
}
```
我们一上来先不着急实现全部功能，先实现leader对follower的匹配点的试探，而匹配点的试探主要由Leader这里prevLogIndex和prevLogTerm这两个参数所实现的。根据论文，只要这个匹配点是相同的，那么之前的所有日志也是相同的。
在我们上一节内容中，每一个follower在收到AppendEntries RPC后会调用AppendEntries这个回调函数，这个函数的作用就是判断这个rpc的任期，把自己变成follower，重置选举时钟。在这一节中，AppendEntries这个函数还需要添加判断匹配点的功能, 具体如下：
如果peer的日志长度小于leader对应的prevLog，我们直接返回false；
如果peer的日志长度大于等于leader对应的prevLog，说明Index是匹配上了，那么我们再查看对应日志条目的任期是否和leader给定的任期相同，如果不同直接返回false，如果相同的话，说明匹配成功，我们需要把参数中的entries追加到peer本机的目录里。
```go
// Peer's callback
// 这个函数是peer在收到来自leader的AppendEntries RPC调用的回调函数
func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	reply.Term = rf.currentTerm
	reply.Success = false

	// align the term
	if args.Term < rf.currentTerm {
		// reply.Success = false
		// fmt.Printf("Server %d in term %d rejected append entries from Server %d in term %d, because of lower term\n", rf.me, rf.currentTerm, args.LeaderId, args.Term)
		return
	}
	if args.Term >= rf.currentTerm {
		rf.becomeFollowerLocked(args.Term)
	}

	// return failure if prevLog not matched
    // 这里先确认，follower的日志长度是否有leader的prevIndex长，如果不够的话直接返回False
	if args.PrevLogIndex >= len(rf.log) {
		fmt.Printf("Server %d in term %d rejected append entries from Server %d in term %d, because of prevLogIndex out of bound\n", rf.me, rf.currentTerm, args.LeaderId, args.Term)
	    return
	}
    // 查看follower对应的索引处的任期是否和leader一致。term和index可以完全确定日志是否相同。
	if rf.log[args.PrevLogIndex].Term != args.PrevLogTerm {
		fmt.Printf("Server %d in term %d rejected append entries from Server %d in term %d, because of prevLogTerm mismatch\n", rf.me, rf.currentTerm, args.LeaderId, args.Term)
		return
	}
	// fmt.Printf("Server %d in term %d received append entries from Server %d in term %d\n", rf.me, rf.currentTerm, args.LeaderId, args.Term)
	
	// after the prevLogIndex is matched, we can append the entries
    // 在确认索引号和任期都相同以后，我们就可以给follower添加日志了
    // 从当前peer服务器的日志中取出索引为 PrevLogIndex 及之前的所有条目。这里使用的是切片操作，它会创建一个新的切片，其中包含从开始到 PrevLogIndex+1 的所有元素。例如，如果 PrevLogIndex 是 3，rf.log[:4] 会返回日志中前四个条目（索引 0, 1, 2, 3）。append(rf.log[:args.PrevLogIndex+1], args.Entries...) 将 args.Entries 中的所有条目追加到步骤1得到的切片后面。
	rf.log = append(rf.log[:args.PrevLogIndex+1], args.Entries...)
	reply.Success = true
	fmt.Printf("Server %d in term %d appended entries from Server %d in term %d\n", rf.me, rf.currentTerm, args.LeaderId, args.Term)
    // TODO: handle LeaderCommit
    // 我们暂时还没有涉及到commit日志的部分，这一功能需要之后实现
}
```

刚刚实现的部分是接收方的代码，对应地我们发送方也需要实现功能。发送方（也就是leader）需要为AppendEntries RPC添加对应的参数，收到返回值后也要进行一系列操作，具体如下：
```go
func (rf *Raft) startReplication(term int) bool {
	// this is the function to send AppendEntries RPCs to one single peer
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
			// fmt.Printf("Server %d failed to send append entries to server %d\n", rf.me, peer)
			return
		}
		// check the term
		if reply.Term > rf.currentTerm {
			// fmt.Printf("Server %d term outdated, current term: %d, reply term: %d\n", rf.me, rf.currentTerm, reply.Term)
			rf.becomeFollowerLocked(reply.Term)
			return
		}

        // leader在得到peer回复后，首先要查看这次AppendEntries RPC是否成功
		if !reply.Success {
            // 如果不成功，就回退一个任期
			// go back a term
			idx, term := args.PrevLogIndex, args.PrevLogTerm
			for idx > 0 && rf.log[idx].Term == term {
				idx--
			}
            // 找到上一个任期的idx，存到这个peer对应的下一次AppendEntries RPC任务中
			rf.nextIndex[peer] = idx + 1
			// fmt.Printf("Server %d nextIndex to server %d is %d\n", rf.me, peer, rf.nextIndex[peer])
		    return
		}

        // 此时已经成功了
		// update the matchIndex and nextIndex if log appended successfully
		rf.matchIndex[peer] = args.PrevLogIndex + len(args.Entries) // important
		rf.nextIndex[peer] = rf.matchIndex[peer] + 1

		// TODO: handle commitIndex
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()
	// check context
	if rf.contextLostLocked(Leader, term) {
		// fmt.Printf("Server %d context lost, not a leader, role: %v, term: %d\n", rf.me, rf.role, rf.currentTerm)
		return false
	}
	for peer := 0; peer < len(rf.peers); peer++ {
		if peer == rf.me {
			// for leader, the match point is always the last log entry
			rf.matchIndex[peer] = len(rf.log) - 1
			rf.nextIndex[peer] = len(rf.log)
			continue
		}
		prevIdx := rf.nextIndex[peer] - 1
		prevTerm := rf.log[prevIdx].Term	
		// construct the append entries args
		args := &AppendEntriesArgs{
			Term: rf.currentTerm,
			LeaderId: rf.me,
			PrevLogIndex: prevIdx,
			PrevLogTerm: prevTerm,
			Entries: append([]LogEntry(nil), rf.log[prevIdx+1:]...),
		}
		go replicateToPeer(peer, args)
	}
	return true
}
```

别忘记初始化nextIndex和matchIndex
```go
func (rf *Raft) becomeLeaderLocked() {
	if (rf.role != Candidate) {
		// must be a candidate to become a leader	
		log.Printf("Can't become leader, Server %d must be a candidate to be a leader", rf.me)
		return
	}
	rf.role = Leader
	for peer := 0; peer < len(rf.peers); peer++ {
		rf.nextIndex[peer] = len(rf.log)
		rf.matchIndex[peer] = 0
	}
	// reset election timer
	rf.resetElectionTimerLocked()
}
```

在领导者选举的部分，针对candidate要票，每一个peer判断是否投票的唯一标准就是这个candidate的任期。这一节有了日志以后，我们就要添加新的判断标准。论文中也提到过，针对每一个peer来说，candidate的任期要新，而且日志也要更新。那么日志是怎么定义谁的日志更新的呢？论文中给出了详细描述：

如果candidate和peer的最后一条日志的任期不一样，谁的任期更高谁就更新；
如果candidate和peer的最后一条日志的任期一样，谁的日志条目数量更多谁就更新。

由此我们可以新写一个函数来判断谁更新
```go
// check whether this peer's last log is more up-to-date than the candidate's last log
func (rf *Raft) isMoreUptoDateLocked(candidateIndex int, candidateTerm int) bool {
	// 首先，拿到当前Peer的最后一条日志索引号以及对应的任期
    l := len(rf.log)
	lastIndex, lastTerm := l - 1, rf.log[l-1].Term
    // 先比较任期
    if lastTerm != candidateTerm {
		return lastTerm > candidateTerm
	}
    // 再比较日志长度
	return lastIndex > candidateIndex
}
```
完成这个函数以后，我们需要在peer接受到candidate的RequestVote RPC调用那里去用这个函数进行比较
这里记得给RequestVote RPC增加应有的参数
```go
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	// 2A
	Term int // candidate's term
	CandidateId int // candidate requesting vote

	// 2B
	LastLogIndex int // index of candidate's last log entry
	LastLogTerm int // term of candidate's last log entry
}

// this function is for server to handle RequestVote RPC from candidate
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).
	rf.mu.Lock()
	defer rf.mu.Unlock()

	// check the term
	// by default, the server will reject the vote request
	reply.Term = rf.currentTerm
	reply.VoteGranted = false
	if args.Term < rf.currentTerm {
		reply.VoteGranted = false
		// fmt.Printf("Server %d in term %d rejected vote request from Server %d in term %d, because of lower term\n", rf.me, rf.currentTerm, args.CandidateId, args.Term)
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
		// fmt.Printf("Server %d in term %d rejected vote request from Server %d in term %d, because it has already voted for Server %d\n", rf.me, rf.currentTerm, args.CandidateId, args.Term, rf.votedFor)
		return
	}

	// check if the candidate's log is more up-to-date
    // 这里利用我们新创建的函数进行检查
	if rf.isMoreUptoDateLocked(args.LastLogIndex, args.LastLogTerm) {
		reply.VoteGranted = false
		// fmt.Printf("Server %d in term %d rejected vote request from Server %d in term %d, because of less up-to-date log\n", rf.me, rf.currentTerm, args.CandidateId, args.Term)
		return
	}

	// we can now vote for the candidate
	reply.VoteGranted = true
	rf.votedFor = args.CandidateId
	rf.resetElectionTimerLocked()
	// fmt.Printf("Server %d in term %d voted for Server %d in term %d\n", rf.me, rf.currentTerm, args.CandidateId, args.Term)
}
```
candidate在进行选举的时候也要在RequestVote RPC中添加好自己的LastIndex和LastTerm
```go
func (rf *Raft) startElection(term int) {
	// Your code here (2A)
	// Send RequestVote RPCs to all other servers.
	// Increment the term.
	// Vote for self.
	// Reset the election timer.
	votes := 0
	askVoteFromPeer := func(peer int, args *RequestVoteArgs) {
		// for every peer, construct an empty reply for them to fill
		reply := &RequestVoteReply{}
		// send the request vote rpc to the peer
		ok := rf.sendRequestVote(peer, args, reply)

		// handle the reply
		rf.mu.Lock()
		defer rf.mu.Unlock()
		// check the response
		if !ok {
			// fmt.Printf("Candidate Server %d failed to get Vote Response from server %d\n", rf.me, peer)
			return
		}
		// response is valid

		// Align the term with the server
		if reply.Term > rf.currentTerm {
			rf.becomeFollowerLocked(reply.Term)
			return
		}

		// check the context
		if rf.contextLostLocked(Candidate, term) {
			// fmt.Printf("Server %d context lost, not starting election\n", rf.me)
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
	};

	rf.mu.Lock()
	defer rf.mu.Unlock()
	// when starting an election, the server should be a candidate and the term should be correct
	if rf.contextLostLocked(Candidate, term) {
		// fmt.Printf("Server %d context lost, not starting election\n", rf.me)
		return
	}
	l := len(rf.log)
	for peer := 0; peer < len(rf.peers); peer++ {
		if peer == rf.me {
			votes++
			continue
		}
		
		// construct the request vote args
		args := &RequestVoteArgs{
			Term: rf.currentTerm,
			CandidateId: rf.me,
			LastLogIndex: l - 1,
			LastLogTerm: rf.log[l-1].Term,
		}

		go askVoteFromPeer(peer, args)	
	}
}
```
在完成了日志复制，选举时日志的比较之后，我们来看日志应用的内容。
首先，我们为raft增加一些状态变量。查看论文figure2，发现有两个变量是我们需要添加的：commitIndex和lastApplied。commitIndex是整个系统中由leader主导的提交的最新的日志序列号，而lastApplied则是这个peer的状态机最新执行的命令序列号。当二者有差值的时候，说明整个系统已经提交的命令要比我们本机的命令多了，这时候我们本机也要执行这个命令，赶上来。
commitIndex：全局日志提交进度
astApplied：本 Peer 日志 apply 进度
当二者不一样的时候，怎么赶上来呢？我们需要定义一个条件变量。此外，每一个raft server在给本机提交命令的时候其实是通过对kv层递交ApplyMsg信息来实现的，因此raft结构体中也要添加对应的Apply Channel
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
	role Role // indicate the current role of the server
	currentTerm int // latest term server has seen
	votedFor int // candidateId that received vote in current term
	electionStart time.Time // time when election started
	electionTimeout time.Duration // timeout for election

	// 2B
	log []LogEntry // log entries; each entry contains command for state machine, and term when entry was received by leader
    // these items are only used by leaders
	nextIndex []int // for each server, index of the next log entry to send to that server	
    matchIndex []int // for each server, index of highest log entry known to be replicated on server	
    // fields for apply loop
    // 日志应用新添加的变量
	commitIndex int // 全局日志提交进度
	lastApplied int // 本 Peer 日志 apply 进度
	applyCh chan ApplyMsg // channel to send committed log entries to the state machine
	applyCond *sync.Cond // condition variable to signal the apply loop
}
```
别忘记对这几个变量在Make的时候进行初始化
```go
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	//...
	// 2B
	rf.log = append(rf.log, LogEntry{}) // a dummy entry for each server to avoid corner cases
	rf.nextIndex = make([]int, len(rf.peers))
	rf.matchIndex = make([]int, len(rf.peers))
	// 2B: apply channel
	rf.applyCh = applyCh
	rf.applyCond = sync.NewCond(&rf.mu)
    rf.commitIndex = 0
	rf.lastApplied = 0
    // ...
	// start ticker goroutine to start elections
	go rf.electionTicker()
    // start ticker goroutine to apply
	go rf.applicationTicker()
}
```

可以看到，在make函数中，我们新增了一个applicationTicker()，那么下面就来实现一下这个ticker
```go
// raft_application.go

func (rf *Raft) applicationTicker() {
	for rf.killed() == false {
		// Your code here (2B)
		rf.mu.Lock()
		// rf.applyCond.Wait() 会让当前线程等待，直到有新的提交日志条目可以应用
		rf.applyCond.Wait()
		// 遍历 rf.log 日志，从 rf.lastApplied + 1 到 rf.commitIndex，将这些日志条目收集到 entries 列表中。然后解锁 rf.mu
        entries := make([]LogEntry, 0)
		for i := rf.lastApplied + 1; i <= rf.commitIndex; i++ {
			entries = append(entries, rf.log[i])
		}
		rf.mu.Unlock()

        // 遍历收集到的 entries，将每个日志条目封装成 ApplyMsg 并发送到 rf.applyCh 通道中。这样状态机就可以应用这些日志条目
		for i, entry := range entries {
			rf.applyCh <- ApplyMsg{
				CommandValid: entry.CommandValid,
				Command:      entry.Command,
				CommandIndex: entry.CommandIndex + 1 + i,
			}
		}

        // 再次加锁，更新 rf.lastApplied 为已经应用的日志条目的数量
		rf.mu.Lock()
		// fmt.Printf("Server %d applied %d entries\n", rf.me, len(entries))
		rf.lastApplied += len(entries)
		rf.mu.Unlock()
	}
}
```
这里有一个小问题：在调用rf.applyCond.Wait()的时候，会持有锁rf.mu。如果commitIndex没有改变，这段代码会一直等待，导致锁一直被占有，导致阻塞。
实际上当rf.applyCond.Wait()被调用时，它实际上会释放互斥锁rf.mu并将当前线程放入等待队列，直到条件变量被通知（这个通知的机制我们还没有实现）。一旦条件变量被通知，当前线程会被唤醒，重新获得互斥锁rf.mu,这意味着在等待期间，锁rf.mu实际上是被释放的，其他线程可以获取这个锁进行操作。
当其他线程提交新的日志条目并更新commitIndex的时候，会调用rf.applyCond.Signal()或rf.applyCond.Broadcast()来通知等待的线程。这时，等待在rf.applyCond.Wait()上的线程会被唤醒，重新获得锁并继续执行。因为在rf.applyCond.Wait()调用期间锁是被释放的，所以即使commitIndex没有改变，等待线程也不会一直持有锁rf.mu，这避免了资源的长时间占用和潜在的死锁问题。

现在又有一个问题：leader什么时候更新commitIndex呢？根据论文figure2，我们可知，超过半数的所有peer的matchIndex的最小值，而且这个index对应的任期刚好是当前leader的任期，只有这样我们才能更新commitIndex为这个index。写一个函数：
```go
func (rf *Raft) getMajorityIndexLocked() int {
	tempIndexes := make([]int, len(rf.peers))
	copy(tempIndexes, rf.matchIndex)
	sort.Ints(tempIndexes)
    majorityIdx := (len(rf.peers) - 1) / 2
	return tempIndexes[majorityIdx]
}
```
这个函数会将leader这一侧所有peer的matchIndex遍历一遍，然后找到恰好是满足majority个数的matchIndex，将这个index作为结果返回过来。

```go
func (rf *Raft) startReplication(term int) bool {
	// this is the function to send AppendEntries RPCs to one single peer
	replicateToPeer := func(peer int, args *AppendEntriesArgs) {
		// ...

		// TODO: handle commitIndex
		majorityMatched := rf.getMajorityIndexLocked()
		if majorityMatched > rf.commitIndex && rf.log[majorityMatched].Term == rf.currentTerm {
			rf.commitIndex = majorityMatched
			rf.applyCond.Signal()
		}
	}

	// ...
}
```
leader这一侧，每当收到来自peer的reply之后，就要检查一遍是否可以更新commitIndex，然后进行更新，同时触发applicationTicker

对应的，follower这里也需要更改。follower通过AppendEntries中的leaderCommit参数来提交自己的日志到自己的状态机内。这里我们根据论文中figure2为AppendEntries RPC添加最后的部分
```go
// AppendEntriesArgs is the struct for AppendEntries RPC arguments
type AppendEntriesArgs struct {
	// ...
    LeaderCommit int // leader's commitIndex
}
```
follower收到leader发来的leaderCommit，更新自己的commitIndex
```go
// update the commit index if needed and indicate the apply loop to apply
        if args.LeaderCommit > rf.commitIndex {
                rf.commitIndex = args.LeaderCommit
                if rf.commitIndex >= len(rf.log) {
                    rf.commitIndex = len(rf.log) - 1
                }
                rf.applyCond.Signal()
        }
```

在测试框架中，我们需要通过Start函数来给leader本地追加日志，通过replicationTicker传给peer。所以这里需要实现一下Start函数
```go
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	// Your code here (2B).
    if rf.role != Leader {
		return 0, 0, false
	}
	rf.log = append(rf.log, LogEntry{
		CommandValid: true,
		Command: command,
		Term: rf.currentTerm,
	})
	// fmt.Printf("Server %d in term %d started command %v\n", rf.me, rf.currentTerm, command)
	// fmt.Printf("this command is at index %d\n", len(rf.log) - 1)

	return len(rf.log) - 1, rf.currentTerm, true
}
```

经过测试，我发现我在选举成为leader以后忘记调用replicationTicker了，这里补上：
```go
// count the votes
		if reply.VoteGranted {
			votes++
			if votes > len(rf.peers)/2 {
				rf.becomeLeaderLocked()
				// start the leader heartbeat
				go rf.replicationTicker(term)
			}
		}
```