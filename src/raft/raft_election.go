package raft

import (
	// "fmt"
	"fmt"
	"math/rand"
	"time"
)

func (rf *Raft) resetElectionTimerLocked() {
	rf.electionStart = time.Now()
	randRange := int64(electionTimeoutUpperBound - electionTimeoutLowerBound)
	rf.electionTimeout = electionTimeoutLowerBound + time.Duration(rand.Int63()%randRange)
}

func (rf *Raft) isElectionTimeout() bool {
	return time.Since(rf.electionStart) > rf.electionTimeout
}

// check whether this peer's last log is more up-to-date than the candidate's last log
func (rf *Raft) isMoreUptoDateLocked(candidateIndex int, candidateTerm int) bool {
	l := len(rf.log)
	lastIndex, lastTerm := l - 1, rf.log[l-1].Term
    if lastTerm != candidateTerm {
		return lastTerm > candidateTerm
	}
	return lastIndex > candidateIndex
}

func (rf *Raft) electionTicker() {
	for rf.killed() == false {

		// Your code here (2A)
		// Check if a leader election should be started.
		rf.mu.Lock()
		if rf.role != Leader && rf.isElectionTimeout() {
			// If the server is not a leader and the election timeout has passed,
			// the server should start an election.
			rf.becomeCandidateLocked()
			rf.resetElectionTimerLocked()
			// Start the election.
			go rf.startElection(rf.currentTerm)
		}
		rf.mu.Unlock()


		// pause for a random amount of time between 50 and 350
		// milliseconds.
		ms := 50 + (rand.Int63() % 300)
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}

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
				go rf.replicationTicker(term)
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

// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
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
	if rf.isMoreUptoDateLocked(args.LastLogIndex, args.LastLogTerm) {
		reply.VoteGranted = false
		fmt.Printf("Server %d in term %d rejected vote request from Server %d in term %d, because of less up-to-date log\n", rf.me, rf.currentTerm, args.CandidateId, args.Term)
		return
	}

	// we can now vote for the candidate
	reply.VoteGranted = true
	rf.votedFor = args.CandidateId
	rf.resetElectionTimerLocked()
	// fmt.Printf("Server %d in term %d voted for Server %d in term %d\n", rf.me, rf.currentTerm, args.CandidateId, args.Term)
}