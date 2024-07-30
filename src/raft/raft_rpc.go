package raft

// example RequestVote RPC arguments structure.
// field names must start with capital letters!
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	// 2A
	Term int // candidate's term
	CandidateId int // candidate requesting vote

	// 2B
	LastLogIndex int // index of candidate's last log entry
	LastLogTerm int // term of candidate's last log entry
}

// example RequestVote RPC reply structure.
// field names must start with capital letters!
type RequestVoteReply struct {
	// Your data here (2A).
	Term int
	VoteGranted bool 
}

// AppendEntriesArgs is the struct for AppendEntries RPC arguments
type AppendEntriesArgs struct {
	// 2A
	Term int
	LeaderId int
	// 2B
	// used to prove the match point
	PrevLogIndex int // index of log entry immediately preceding new ones
	PrevLogTerm int // term of prevLogIndex entry
	Entries []LogEntry // log entries to store (empty for heartbeat; may send more than one for efficiency)
    LeaderCommit int // leader's commitIndex
}

// AppendEntriesReply is the struct for AppendEntries RPC reply
type AppendEntriesReply struct {
	Term int
	Success bool
}