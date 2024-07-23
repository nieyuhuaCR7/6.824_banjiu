package raft

import (
	// "fmt"
	"time"
)

func (rf *Raft) replicationTicker(term int) {
	for rf.killed() == false {
        ok := rf.startReplication(term)
		if !ok {
			break
		}
		time.Sleep(replicationInterval)
	}
}

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
		// fmt.Printf("Server %d in term %d rejected append entries from Server %d in term %d, because of lower term\n", rf.me, rf.currentTerm, args.LeaderId, args.Term)
		return
	}
	rf.becomeFollowerLocked(args.Term)
	// fmt.Printf("Server %d in term %d received append entries from Server %d in term %d\n", rf.me, rf.currentTerm, args.LeaderId, args.Term)
	reply.Success = true
}

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