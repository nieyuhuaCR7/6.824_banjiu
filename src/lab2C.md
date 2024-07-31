# lab2C

完成lab2B的日志同步功能后，我们来看状态持久化的部分

实验框架提供了一个persister.go的文件，为我们提供了一个persister的结构体，可以存储和读取状态值。Persister结构体包含一个互斥锁，一个字节切片用于保存raft的状态，一个字节切片保存kv服务器的快照。我们主要会用到ReadRaftState()和Save(raftstate []byte, snapshot []byte) 这两个函数。ReadRaftState()可以理解为从磁盘上读出一个raft的字节数组，而Save可以理解为把bytes形式的raft状态写入磁盘中。测试框架在调用raft.Make()时会提供一个Persister。题目要求我们完成raft.go里面的persist()和readPersist()方法，我们同样需要将raft的状态序列化为字节数组，序列化的方法已经给我们了，在labgob.go中。raft.go中也给了我们示例代码
encoding:
```go
// 新建一个bytes.Buffer，用于临时存储序列化后的数据。bytes.Buffer 是一个在内存中读写字节的缓冲区，可以动态扩展容量。
w := new(bytes.Buffer)
// 创建一个序列化的实例
e := labgob.NewEncoder(w)
// 将raft server的各项内容都存进去
e.Encode(rf.xxx)
e.Encode(rf.yyy)
// 调用 w.Bytes() 方法，从 bytes.Buffer 中获取序列化后的字节切片 raftstate。这个切片包含了 rf.xxx 和 rf.yyy 的序列化数据。
raftstate := w.Bytes()
// 将序列化后的 Raft 状态数据 raftstate 保存到持久化存储中。
rf.persister.Save(raftstate, nil)
```
decoding:
```go
// 使用 data（之前持久化存储中的字节切片）创建一个新的 bytes.Buffer 实例 r。data 是从持久化存储中读取的序列化数据
r := bytes.NewBuffer(data)
// 使用 labgob.NewDecoder(r) 创建一个新的 labgob.LabDecoder 实例
d := labgob.NewDecoder(r)
// 定义两个变量 xxx 和 yyy，用于存储反序列化后的数据。它们对应于之前序列化并保存到持久化存储中的 Raft 服务器状态字段
var xxx
var yyy
// 使用 d.Decode(&xxx) 和 d.Decode(&yyy) 将数据从 bytes.Buffer 中解码（反序列化）并存储到变量 xxx 和 yyy 中
if d.Decode(&xxx) != nil ||
    d.Decode(&yyy) != nil {
    error...
} else {
    rf.xxx = xxx
    rf.yyy = yyy
}
```
论文中figure2对raft server需要被持久化的变量也做了描述，currentTerm, votedFor, log[]这三个变量需要被持久化，因此我们根据上面的范例代码完成persist函数
```go
func (rf *Raft) persistLocked() {
	// Your code here (2C).
	// Example:
	// w := new(bytes.Buffer)
	// e := labgob.NewEncoder(w)
	// e.Encode(rf.xxx)
	// e.Encode(rf.yyy)
	// raftstate := w.Bytes()
	// rf.persister.Save(raftstate, nil)
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(rf.currentTerm)
	e.Encode(rf.votedFor)
	e.Encode(rf.log)
	raftstate := w.Bytes()
	// TODO: add the snapshot for part 2D
	rf.persister.Save(raftstate, nil)
}
```
同样地，我们也完成readPersist这个函数
```go
// restore previously persisted state.
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	// Your code here (2C).
	// Example:
	// r := bytes.NewBuffer(data)
	// d := labgob.NewDecoder(r)
	// var xxx
	// var yyy
	// if d.Decode(&xxx) != nil ||
	//    d.Decode(&yyy) != nil {
	//   error...
	// } else {
	//   rf.xxx = xxx
	//   rf.yyy = yyy
	// }
	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)
	var currentTerm int
	var votedFor int
	var log []LogEntry
	if err := d.Decode(&currentTerm); err != nil {
		fmt.Printf("Server %d failed to decode currentTerm\n", rf.me)
		return
	}
	rf.currentTerm = currentTerm
	if err := d.Decode(&votedFor); err != nil {
		fmt.Printf("Server %d failed to decode votedFor\n", rf.me)
		return
	}
	rf.votedFor = votedFor
	if err := d.Decode(&log); err != nil {
		fmt.Printf("Server %d failed to decode log\n", rf.me)
		return
	}
	rf.log = log
    fmt.Printf("Server %d successfully readPersist, currentTerm: %d, votedFor: %d, log length: %d\n", rf.me, rf.currentTerm, rf.votedFor, len(rf.log))
}
```
我们实现的这两个函数，在raft中需要被调用。对于readPersist函数来说，调用比较简单，只需要在实验框架调用Make()的时候读取已经存储的状态就可以了，基础代码中已经帮我们调用了这部分。而persistLocked函数的调用相对复杂一点，由于它存储了三个变量，所以我们需要在这三个变量被修改的时候调用persist函数
在lab2A的时候，我们写过三个状态转移的函数，这里我们检查一下它们是否需要添加persistLocked。经过检查，我发现becomeFollowerLocked和becomeCandidateLocked需要加上persistLocked()
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
	shouldPersist := rf.currentTerm != term
	if term > rf.currentTerm {
		rf.votedFor = -1
	}
    // 记得将自己的role和任期进行更新
    rf.role = Follower
	rf.currentTerm = term
	// reset election timer
	if shouldPersist {
		rf.persistLocked()
	}
	rf.resetElectionTimerLocked()
}

func (rf *Raft) becomeCandidateLocked() {
	if (rf.role == Leader) {
		// already a leader	
		log.Printf("Can't become candidate, Server %d is already a leader", rf.me)
		return
	}
	rf.role = Candidate
	rf.currentTerm++
	// fmt.Printf("Server %d in term %d became candidate\n", rf.me, rf.currentTerm)
	rf.votedFor = rf.me
	rf.persistLocked()
	// reset election timer
	rf.resetElectionTimerLocked()
}
```
在选举过程中，如果一个raft server决定给某个candidate投票，那么它的votedFor变量会被改变，这个函数是在RequestVote的回调函数中实现的
```go
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).
    rf.mu.Lock()
	defer rf.mu.Unlock()
	// ...
	// we can now vote for the candidate
	reply.VoteGranted = true
	rf.votedFor = args.CandidateId
	rf.persistLocked()
	rf.resetElectionTimerLocked()
	// fmt.Printf("Server %d in term %d voted for Server %d in term %d\n", rf.me, rf.currentTerm, args.CandidateId, args.Term)
}
```
follower在接受到leader的日志更新的时候，也需要persistLocked()
```go
func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	// ...
	
	// after the prevLogIndex is matched, we can append the entries
	rf.log = append(rf.log[:args.PrevLogIndex+1], args.Entries...)
	rf.persistLocked()
	reply.Success = true
	// ...
}
```
应用层在调用leader的Start函数时，为leader提交日志，这里也需要实现persistLocked()
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
	rf.persistLocked()
	// fmt.Printf("Server %d in term %d started command %v\n", rf.me, rf.currentTerm, command)
	// fmt.Printf("this command is at index %d\n", len(rf.log) - 1)

	return len(rf.log) - 1, rf.currentTerm, true
}
```
由于这次的代码量较小，而且我们没怎么修改之前的代码，2C的测试顺利通过