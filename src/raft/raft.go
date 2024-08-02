package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	//	"bytes"
	// "bytes"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	//	"6.5840/labgob"
	// "6.5840/labgob"
	"6.5840/labrpc"
	// "github.com/tidwall/match"
)

const (
	InvalidTerm  int = 0
	InvalidIndex int = 0
)

type Role string

const (
	Follower  Role = "Follower"
	Candidate Role = "Candidate"
	Leader    Role = "Leader"
)

const (
	electionTimeoutLowerBound time.Duration = 250 * time.Millisecond
	electionTimeoutUpperBound time.Duration = 400 * time.Millisecond
	replicationInterval time.Duration = 70 * time.Millisecond
)


// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
//
// in part 2D you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh, but set CommandValid to false for these
// other uses.
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int

	// For 2D:
	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  int
	SnapshotIndex int
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
	// log []LogEntry // log entries; each entry contains command for state machine, and term when entry was received by leader
    log *RaftLog // 2D
	// these items are only used by leaders
	nextIndex []int // for each server, index of the next log entry to send to that server	
    matchIndex []int // for each server, index of highest log entry known to be replicated on server	
    // fields for apply loop
	commitIndex int // index of highest log entry known to be committed
	lastApplied int // index of highest log entry applied to this peer's state machine
	applyCh chan ApplyMsg // channel to send committed log entries to the state machine
	applyCond *sync.Cond // condition variable to signal the apply loop

	// 2D
	snapPending bool
}

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

func (rf *Raft) becomeLeaderLocked() {
	if (rf.role != Candidate) {
		// must be a candidate to become a leader	
		log.Printf("Can't become leader, Server %d must be a candidate to be a leader", rf.me)
		return
	}
	rf.role = Leader
	// fmt.Printf("Server %d in term %d became leader\n", rf.me, rf.currentTerm)
	for peer := 0; peer < len(rf.peers); peer++ {
		rf.nextIndex[peer] = rf.log.size()
		rf.matchIndex[peer] = 0
	}
	// reset election timer
	rf.resetElectionTimerLocked()
}

// this function is to check the context of the server is lost or not
func (rf *Raft) contextLostLocked(role Role, term int) bool {
	return rf.role != role || rf.currentTerm != term
}

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


// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	// Your code here (2B).
    if rf.role != Leader {
		return 0, 0, false
	}
	rf.log.append(LogEntry{
		CommandValid: true,
		Command: command,
		Term: rf.currentTerm,
	})
	rf.persistLocked()
	// fmt.Printf("Server %d in term %d started command %v\n", rf.me, rf.currentTerm, command)
	// fmt.Printf("this command is at index %d\n", len(rf.log) - 1)

	return rf.log.size() - 1, rf.currentTerm, true
}

// the tester doesn't halt goroutines created by Raft after each test,
// but it does call the Kill() method. your code can use killed() to
// check whether Kill() has been called. the use of atomic avoids the
// need for a lock.
//
// the issue is that long-running goroutines use memory and may chew
// up CPU time, perhaps causing later tests to fail and generating
// confusing debug output. any goroutine with a long-running loop
// should call killed() to check whether it should stop.
func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
	// Your code here, if desired.
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
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
	// rf.log = append(rf.log, LogEntry{}) // a dummy entry for each server to avoid corner cases
	rf.log = NewLog(InvalidIndex, InvalidTerm, nil, nil)
	rf.nextIndex = make([]int, len(rf.peers))
	rf.matchIndex = make([]int, len(rf.peers))
	// 2B: apply channel
	rf.applyCh = applyCh
	rf.applyCond = sync.NewCond(&rf.mu)
	rf.commitIndex = 0
	rf.lastApplied = 0

	// 2D
	rf.snapPending = false

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())
	fmt.Printf("Server %d in term %d started\n", rf.me, rf.currentTerm)

	// start ticker goroutine to start elections
	go rf.electionTicker()
    // start ticker goroutine to apply
	go rf.applicationTicker()

	return rf
}
