package raft

// example RequestVote RPC arguments structure.
// field names must start with capital letters!
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	Term int
	CandidateId int
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
	Term int
	LeaderId int
}

// AppendEntriesReply is the struct for AppendEntries RPC reply
type AppendEntriesReply struct {
	Term int
	Success bool
}