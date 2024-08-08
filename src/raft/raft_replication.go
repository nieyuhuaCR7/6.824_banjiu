package raft

import (
	// "fmt"
	"fmt"
	"sort"
	"time"
)

type LogEntry struct {
	Term int // term when the command was received by the leader
	CommandValid bool // whether the command is valid
	Command interface{} // the command
	// CommandIndex int // the index of the command
}

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
// Peer's callback
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
	if args.PrevLogIndex >= rf.log.size() {
		// fmt.Printf("Server %d in term %d rejected append entries from Server %d in term %d, because of prevLogIndex out of bound\n", rf.me, rf.currentTerm, args.LeaderId, args.Term)
	    return
	}
	// // print the start and the end of the server's log
	// fmt.Printf("Server %d log start: %v\n", rf.me, rf.log.at(0))
	// fmt.Printf("Server %d log end: %v\n", rf.me, rf.log.at(rf.log.size() - 1))
	// print the args's prevLogIndex and prevLogTerm
	// fmt.Printf("Server %d in term %d received append entries from Server %d in term %d, prevLogIndex: %d, prevLogTerm: %d\n", rf.me, rf.currentTerm, args.LeaderId, args.Term, args.PrevLogIndex, args.PrevLogTerm)
	if args.PrevLogIndex < rf.log.snapLastIdx {
		return
	}
	if rf.log.at(args.PrevLogIndex).Term != args.PrevLogTerm {
		fmt.Printf("Server %d in term %d rejected append entries from Server %d in term %d, because of prevLogTerm mismatch\n", rf.me, rf.currentTerm, args.LeaderId, args.Term)
		return
	}
	// fmt.Printf("Server %d in term %d received append entries from Server %d in term %d\n", rf.me, rf.currentTerm, args.LeaderId, args.Term)
	
	// after the prevLogIndex is matched, we can append the entries
	// rf.log = append(rf.log[:args.PrevLogIndex+1], args.Entries...)
	rf.log.appendFrom(args.PrevLogIndex, args.Entries)
	rf.persistLocked()
	reply.Success = true
	// fmt.Printf("Server %d in term %d appended entries from Server %d in term %d\n", rf.me, rf.currentTerm, args.LeaderId, args.Term)
    // TODO: handle LeaderCommit
	if args.LeaderCommit > rf.commitIndex {
		rf.commitIndex = args.LeaderCommit
		// if rf.commitIndex >= len(rf.log) {
		// 	rf.commitIndex = len(rf.log) - 1
		// }
		rf.applyCond.Signal()
	}

	rf.resetElectionTimerLocked()
}

func (rf *Raft) getMajorityIndexLocked() int {
	tempIndexes := make([]int, len(rf.peers))
	copy(tempIndexes, rf.matchIndex)
	sort.Ints(tempIndexes)
    majorityIdx := (len(rf.peers) - 1) / 2
	return tempIndexes[majorityIdx]
}

func (rf *Raft) startReplication(term int) bool {
	// this is the function to send AppendEntries RPCs to one single peer
	// fmt.Printf("Server %d in term %d starting replication\n", rf.me, rf.currentTerm)
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

		// check the context
		if rf.contextLostLocked(Leader, term) {
			fmt.Printf("Server %d context lost, not a leader, role: %v, term: %d\n", rf.me, rf.role, rf.currentTerm)
			return
		}

		if !reply.Success {
			// go back a term
			idx := rf.nextIndex[peer] - 1
			// avoid the snapshotted entries, which are not in the log
			if idx < rf.log.snapLastIdx {
				rf.nextIndex[peer] = rf.log.snapLastIdx + 1
				// fmt.Printf("Server %d nextIndex to server %d is %d\n", rf.me, peer, rf.nextIndex[peer])
				return
			}
			term := rf.log.at(idx).Term
			for idx >= rf.log.snapLastIdx && rf.log.at(idx).Term == term {
				idx--
			}
			rf.nextIndex[peer] = idx + 1
			nextPrevIdx := rf.nextIndex[peer] - 1
			nextPrevTerm := InvalidTerm
			if nextPrevIdx >= rf.log.snapLastIdx {
					nextPrevTerm = rf.log.at(nextPrevIdx).Term
			}
			fmt.Printf("Server %d in term %d rejected append entries from Server %d in term %d, because of prevLogTerm mismatch\n", rf.me, rf.currentTerm, peer, nextPrevTerm)
			// fmt.Printf("Server %d nextIndex to server %d is %d\n", rf.me, peer, rf.nextIndex[peer])
		    return
		}

		// update the matchIndex and nextIndex if log appended successfully
		rf.matchIndex[peer] = args.PrevLogIndex + len(args.Entries) // important
		rf.nextIndex[peer] = rf.matchIndex[peer] + 1

		// TODO: handle commitIndex
		majorityMatched := rf.getMajorityIndexLocked()
		if majorityMatched > rf.commitIndex && rf.log.at(majorityMatched).Term == rf.currentTerm {
			rf.commitIndex = majorityMatched
			rf.applyCond.Signal()
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
			// for leader, the match point is always the last log entry
			rf.matchIndex[peer] = rf.log.size() - 1
			rf.nextIndex[peer] = rf.log.size()
			continue
		}
		prevIdx := rf.nextIndex[peer] - 1
		if prevIdx < rf.log.snapLastIdx {
			// send InstallSnapshot RPC
			args := &InstallSnapshotArgs{
				Term: rf.currentTerm,
				LeaderId: rf.me,
				LastIncludedIndex: rf.log.snapLastIdx,
				LastIncludedTerm: rf.log.snapLastTerm,
				Snapshot: rf.log.snapshot,
			}
			// fmt.Printf("Server %d in term %d sending install snapshot to Server %d\n", rf.me, rf.currentTerm, peer)
		    go rf.installOnPeer(peer, term, args)
			continue
		}
		prevTerm := rf.log.at(prevIdx).Term	
		// construct the append entries args
		args := &AppendEntriesArgs{
			Term: rf.currentTerm,
			LeaderId: rf.me,
			PrevLogIndex: prevIdx,
			PrevLogTerm: prevTerm,
			Entries: rf.log.tail(prevIdx + 1),
			LeaderCommit: rf.commitIndex,
		}
		// fmt.Printf("Server %d in term %d sending append entries to Server %d\n", rf.me, rf.currentTerm, peer)
		go replicateToPeer(peer, args)
	}
	return true
}