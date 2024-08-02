package raft

// the service says it has created a snapshot that has
// all info up to and including index. this means the
// service no longer needs the log through (and including)
// that index. Raft should now trim its log as much as possible.
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	// Your code here (2D).
    rf.mu.Lock()
	defer rf.mu.Unlock()

	if index > rf.commitIndex {
		return
    }
	if index <= rf.log.snapLastIdx {
		return
	}

	rf.log.doSnapshot(index, snapshot)
	rf.persistLocked()
}

// this function is for follower to handle InstallSnapshot RPC from leader
func (rf *Raft) InstallSnapshot(args *InstallSnapshotArgs, reply *InstallSnapshotReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

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
	rf.log.installSnapshot(args.LastIncludedIndex, args.LastIncludedTerm, args.Snapshot)
	rf.persistLocked()
	rf.snapPending = true
	rf.applyCond.Signal()
}

// for leader to send InstallSnapshot RPC to follower
func (rf *Raft) sendInstallSnapshot(server int, args *InstallSnapshotArgs, reply *InstallSnapshotReply) bool {
	// Your code here (2D).
	ok := rf.peers[server].Call("Raft.InstallSnapshot", args, reply)
	return ok
}

func (rf *Raft) installOnPeer(peer int, term int, args *InstallSnapshotArgs) {
	// Your code here (2D).
	reply := &InstallSnapshotReply{}
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