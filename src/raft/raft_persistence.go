package raft

import (
	"bytes"
	"fmt"

	"6.5840/labgob"
)

// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
// before you've implemented snapshots, you should pass nil as the
// second argument to persister.Save().
// after you've implemented snapshots, pass the current snapshot
// (or nil if there's not yet a snapshot).
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