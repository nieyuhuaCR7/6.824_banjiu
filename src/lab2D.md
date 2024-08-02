# lab2D

完成lab2C的状态持久化功能后，我们来看日志压缩的部分

前面提到，我们的raft server对应的是一个kv存储引擎，形成共识的是发给每个服务器的命令日志以及顺序。但是随着时间的推移，日志积攒太多容易超出磁盘空间限制。存放kv引擎的状态要比存放日志本身要节省很多空间。因此实验框架要求我们实现日志压缩快照功能。如果定期对日志做快照(snapshot)，针对某个日志条目做了快照以后，该条目之前的日志都可以被删除，因为我们已经得到kv状态了。论文中还提到一个问题，如果leader已经做了快照删掉一些日志，而follower差的太远，需要被同步的日志已经被删除了，这时我们可以使用一个InstallSnapshot RPC来应对这个问题。leader通过这个rpc来发送快照给follower，使得它们可以同步状态。

在之前几个lab中，我们在raft结构体里面定义了log []LogEntry，说明之前服务器中存储的全部是日志命令，从第一条到最后一条；而现在则不一样，server会存储一个snapshot，加上剩下的日志，因此我们重新定义日志：
```go
type RaftLog struct {
	snapLastIdx int
	snapLastTerm int

	// contains [1, snapLastIdx]
	snapshot []byte
	tailLog []LogEntry
}
```
这个结构体主要包含三部分：
前面日志截断后compact而成的snapshot
后面的剩余日志tailLog
二者的分界线 snapLastIdx/snapLastTerm

为这个结构体写一个构造函数
```go
func NewLog(snapLastIdx, snapLastTerm int, snapshot []byte, entries []LogEntry) *RaftLog {
	rl := &RaftLog{
		snapLastIdx:  snapLastIdx,
		snapLastTerm: snapLastTerm,
		snapshot:     snapshot,
	}

	// 创建一个长度为0， 容量为1 + len(entries)的LogEntry切片
    rl.tailLog = make([]LogEntry, 0, 1+len(entries))
	rl.tailLog = append(rl.tailLog, LogEntry{Term: snapLastTerm})
	rl.tailLog = append(rl.tailLog, entries...)

	return rl
}
```

同样地，需要实现这个结构体的序列化和反序列化函数，这个主要是为了和上一节lab对应上
```go
func (rl *RaftLog) readPersist(d *labgob.LabDecoder) error {
	var lastIdx int
	if err := d.Decode(&lastIdx); err != nil {
		return fmt.Errorf("decode last include index failed")
	}
	rl.snapLastIdx = lastIdx

	var lastTerm int
	if err := d.Decode(&lastTerm); err != nil {
		return fmt.Errorf("decode last include term failed")
	}
	rl.snapLastTerm = lastTerm

	var log []LogEntry
	if err := d.Decode(&log); err != nil {
		return fmt.Errorf("decode log failed")
	}
	rl.tailLog = log

	return nil
}

func (rl *RaftLog) persist(e *labgob.LabEncoder) {
	e.Encode(rl.snapLastIdx)
	e.Encode(rl.snapLastTerm)
	e.Encode(rl.tailLog)
}
```

size函数用来返回这个服务器至今已经有多少个日志。之前我们可以直接返回rf.log.length(), 现在要加上snapshot占用的日志数量和目前没有被快照的日志数量。返回当前日志的总大小，rl.snapLastIdx 表示快照中包含的最后一个日志条目的索引，len(rl.tailLog) 表示在 tailLog 切片中存储的日志条目的数量，两者相加就是整个日志的逻辑大小
```go
func (rl *RaftLog) size() int {
	return rl.snapLastIdx + len(rl.tailLog)
}
```
将逻辑索引转换为 tailLog 切片中的实际索引。
在实际操作中，因为我们日志中的tailLog仅仅保存最后一点日志条目，而对于外界来说我们想访问的那条日志的下标很有可能没有存储在我们实际的tailLog里面，而且里面的逻辑地址和实际地址还需要进行转化，因此我们写一个函数：
```go
func (rl *RaftLog) idx(logicIdx int) int {
	// if the logicIdx fall beyond [snapLastIdx, size() - 1]
    // 一旦越界直接报错
	if logicIdx < rl.snapLastIdx || logicIdx >= rl.size() {
		panic(fmt.Sprintf("logicIdx %d out of range [%d, %d)", logicIdx, rl.snapLastIdx, rl.size()))
	}
	return logicIdx - rl.snapLastIdx
}
```
at方法返回指定逻辑索引处的日志条目，先通过idx方法将逻辑索引转化为tailLog中的实际索引，然后返回该索引处的日志条目。
```go
func (rl *RaftLog) at(logicIdx int) LogEntry {
	return rl.tailLog[rl.idx(logicIdx)]
}
```

partD的一个任务就是完成raft服务器提供给应用层的Snapshot功能。大概的意思就是，当实验框架调用这个函数时，应用层告诉raft服务器，已经在index处做了一个快照，所以raft层不需要保存前面的日志了。应用层把日志的snapshot发给raft层，保证它可以进行AppendEntries RPC。所以我们raft层在接到这个命令的时候就要把日志截断，释放空间，同时把snapshot存起来。这里我们先不实现InstallSnapshot RPC。可以看到，我们为raftLog新增了snapshotIndex以及snapshot的内容，替换掉之前的log, 所以这里需要把之前的代码整体稍作修改。同样我们也要实现Snapshot的截断日志的操作

```go
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	// Your code here (2D).
    rf.mu.Lock()
	defer rf.mu.Unlock()

	rf.log.doSnapshot(index, snapshot)
	rf.persistLocked()
}

func (rl *RaftLog) doSnapshot(index int, snapshot []byte) {
	idx := rl.idx(index)

	rl.snapLastIdx = index
	rl.snapLastTerm = rl.tailLog[idx].Term
	rl.snapshot = snapshot

	// make a new log array
	newLog := make([]LogEntry, 0, rl.size() - rl.snapLastIdx)
	newLog = append(newLog, LogEntry{
		Term: rl.snapLastTerm,
	})
	newLog = append(newLog, rl.tailLog[idx+1:]...)
	rl.tailLog = newLog
}

```

对于SnapShot功能来说，主要的数据流是这样的：Leader的应用层调用raft.Snapshot(index, snapshot)函数来保存leader raft的快照，截断日志并持久化。leader接着正常和它的follower进行通信，一旦有follower错过的太多，leader就启用InstallSnapshot RPC来和follower通信，follower收到这个rpc后替换本地日志，持久化，最后follower通过ApplyMsg将snapshot传给follower的应用层。

我们可以把有关日志压缩的代码全部放到一个raft_conpaction.go的文件中

注意到，和上一节相比，我们这一节中持久化的部分要多一个snapshot，所以我们需要在persistLocked函数和readPersist函数中添加这一字段。Persister类型中已经为我们提供了这个方法，所以我们直接调用就可
```go
func (rf *Raft) persistLocked() {
	//...
	// TODO: add the snapshot for part 2D
	// DONE
	rf.persister.Save(raftstate, rf.log.snapshot)
}

func (rf *Raft) readPersist(data []byte) {
	// ...
	rf.log.snapshot = rf.persister.ReadSnapshot()
    // ...
}
```

思考一下，在什么情况下，leader需要给follower发送InstallSnapshot RPC呢？当然是follower当前的进度已经远远落后于leader，以至于leader的snapLastIdx已经大于follower对应的prevIdx，此时leader需要给follower发送InstallSnapshot RPC，而不是AppendEntries RPC。那么我们就来定义一下InstallSnapshot RPC， 根据论文，我们可以定义这样的RPC:
```go
type InstallSnapshotArgs struct {
	Term int
	LeaderId int

	LastIncludedIndex int
	LastIncludedTerm int

	Snapshot []byte
}

type InstallSnapshotReply struct {
	Term int
}
```
InstallSnapshot RPC其实很像AppendEntries RPC，在leader replicate给其他follower时，如果发现nextIndex小于自己当前日志snapshotLastIdx,说明follower所需要的日志我们leader已经存到快照里面了，此时就应该给这个follower发一个installSnapshot RPC，因此我们在startReplication这个函数里添加：
```go
for peer := 0; peer < len(rf.peers); peer++ {
		if peer == rf.me {
			// for leader, the match point is always the last log entry
			rf.matchIndex[peer] = rf.log.size() - 1
			rf.nextIndex[peer] = rf.log.size()
			continue
		}
		prevIdx := rf.nextIndex[peer] - 1
        // 判断一下我们本机有没有它所需要的日志
		if prevIdx < rf.log.snapLastIdx {
			// send InstallSnapshot RPC
			args := &InstallSnapshotArgs{
				Term: rf.currentTerm,
				LeaderId: rf.me,
				LastIncludedIndex: rf.log.snapLastIdx,
				LastIncludedTerm: rf.log.snapLastTerm,
				Snapshot: rf.log.snapshot,
			}
			fmt.Printf("Server %d in term %d sending install snapshot to Server %d\n", rf.me, rf.currentTerm, peer)
            // 新开一个go协程，给follower发送InstallSnapshot RPC
		    go rf.installToPeer(peer, term, args)
		}
```
startReplication函数里面，如果发现日志不够了，就会调用一个installToPeer的函数，这个函数实际上和replicateToPeer是差不多的，都是构造好一个reply，调用peer的handle rpc函数，然后处理结果，因此我们有：
```go
func (rf *Raft) installToPeer(peer int, term int, args *InstallSnapshotArgs) {
	// Your code here (2D).
    // 构造一个peer的空reply
	reply := &InstallSnapshotReply{}
    // 发送rpc，调用peer的handle函数
	ok := rf.sendInstallSnapshot(peer, args, reply)

	rf.mu.Lock()
	defer rf.mu.Unlock()
	if !ok {
		return
	}
	
	// align the term
    if reply.Term > rf.currentTerm {
		rf.becomeFollowerLocked(reply.Term)
		return
	}

	// check if the context is lost
	if rf.contextLostLocked(Leader, term) {
		return
	}

	// update the nextIndex and matchIndex
	if args.LastIncludedIndex > rf.matchIndex[peer] {
		rf.matchIndex[peer] = args.LastIncludedIndex
		rf.nextIndex[peer] = rf.matchIndex[peer] + 1
	}
}
```

sendInstallSnapshot函数也很像sendAppendEntries函数，其实这些函数长得都大差不差, 都是调用Peer的handle函数:
```go
// for leader to send InstallSnapshot RPC to follower
func (rf *Raft) sendInstallSnapshot(server int, args *InstallSnapshotArgs, reply *InstallSnapshotReply) bool {
	// Your code here (2D).
	ok := rf.peers[server].Call("Raft.InstallSnapshot", args, reply)
	return ok
}
```
那么我们也同时给出Peer的handle函数：

```go
// this function is for follower to handle InstallSnapshot RPC from leader
func (rf *Raft) InstallSnapshot(args *InstallSnapshotArgs, reply *InstallSnapshotReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	// follower先把reply中空缺的任期填好
    reply.Term = rf.currentTerm
	// align the term
	if args.Term < rf.currentTerm {
		return
	}
	if args.Term >= rf.currentTerm {
		rf.becomeFollowerLocked(args.Term)
	}

	// check if there is already a snapshot with a larger index
	if rf.log.snapLastIdx >= args.LastIncludedIndex {
		return
	}

	// install the snapshot
    // 这里调用了follower自身log的installSnapshot函数，利用leader给的snapshot重置自己的log
	rf.log.installSnapshot(args.LastIncludedIndex, args.LastIncludedTerm, args.Snapshot)
	rf.persistLocked()
    // 这里表明follower本身进行了一个snapshot，需要被applicationTicker觉察到，提交到自己的应用层
	rf.snapPending = true
	rf.applyCond.Signal()
}
```
log的installSnapshot函数是针对leader的InstallSnapshot RPC的snapshot，由外而内改变自己的状态
```go
// install snapshot for raft layer
func (rl *RaftLog) installSnapshot(index, term int, snapshot []byte) {
	rl.snapLastIdx = index
	rl.snapLastTerm = term
	rl.snapshot = snapshot

	// make a new log array
	// just discard all the local log, and use the leader's snapshot
	newLog := make([]LogEntry, 0, 1)
	newLog = append(newLog, LogEntry{
			Term: rl.snapLastTerm,
	})
	rl.tailLog = newLog
}
```
我们前面提过，follower在raft层持久化后，还需要跟应用层沟通一下，这里扩写一下applicationTicker：
```go
func (rf *Raft) applicationTicker() {
	for rf.killed() == false {
		// Your code here (2B)
		rf.mu.Lock()
		rf.applyCond.Wait()
		
        entries := make([]LogEntry, 0)
		snapPendingApply := rf.snapPending

		if !snapPendingApply {
			for i := rf.lastApplied + 1; i <= rf.commitIndex; i++ {
				entries = append(entries, rf.log.at(i))
			}
		}
		rf.mu.Unlock()

		if !snapPendingApply {
			for i, entry := range entries {
				rf.applyCh <- ApplyMsg{
					CommandValid: entry.CommandValid,
					Command:      entry.Command,
					CommandIndex: rf.lastApplied + 1 + i,
				}
			}
		} else {
			rf.applyCh <- ApplyMsg{
				SnapshotValid: true,
				Snapshot: rf.log.snapshot,
				SnapshotTerm: rf.log.snapLastTerm,
				SnapshotIndex: rf.log.snapLastIdx,
			}
		}

		rf.mu.Lock()
		if !snapPendingApply {
			// fmt.Printf("Server %d applied %d entries\n", rf.me, len(entries))
			rf.lastApplied += len(entries)
			
		} else {
			// fmt.Printf("Server %d applied snapshot\n", rf.me)
			rf.lastApplied = rf.log.snapLastIdx
			rf.snapPending = false
			if rf.lastApplied < rf.commitIndex {
				rf.commitIndex = rf.lastApplied
			}
			rf.snapPending = false
		}
		rf.mu.Unlock()
	}
}
```
partD非常复杂，写得我头疼