package raft

import (
	"fmt"

	"6.5840/labgob"
)

type RaftLog struct {
	snapLastIdx int
	snapLastTerm int

	// contains [1, snapLastIdx]
	snapshot []byte
	tailLog []LogEntry
}

func NewLog(snapLastIdx, snapLastTerm int, snapshot []byte, entries []LogEntry) *RaftLog {
	rl := &RaftLog{
		snapLastIdx: snapLastIdx,
		snapLastTerm: snapLastTerm,
		snapshot: snapshot,
	}
	
	rl.tailLog = make([]LogEntry, 0, 1 + len(entries))
	rl.tailLog = append(rl.tailLog, LogEntry{Term: snapLastTerm})
	rl.tailLog = append(rl.tailLog, entries...)

	return rl
}

// all the functions below should be called with lock held
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

// index conversion
func (rl *RaftLog) size() int {
	return rl.snapLastIdx + len(rl.tailLog)
}

func (rl *RaftLog) idx(logicIdx int) int {
	// if the logicIdx fall beyond [snapLastIdx, size() - 1]
	if logicIdx < rl.snapLastIdx || logicIdx >= rl.size() {
		panic(fmt.Sprintf("logicIdx %d out of range [%d, %d)", logicIdx, rl.snapLastIdx, rl.size()))
	}
	return logicIdx - rl.snapLastIdx
}

func (rl *RaftLog) at(logicIdx int) LogEntry {
	return rl.tailLog[rl.idx(logicIdx)]
}

// String
func (rl *RaftLog) String() string {
	var terms string
	prevTerm := rl.snapLastTerm
	prevStart := rl.snapLastIdx
	for i := 0; i < len(rl.tailLog); i++ {
			if rl.tailLog[i].Term != prevTerm {
					terms += fmt.Sprintf(" [%d, %d]T%d", prevStart, i-1, prevTerm)
					prevTerm = rl.tailLog[i].Term
					prevStart = i
			}
	}
	terms += fmt.Sprintf("[%d, %d]T%d", prevStart, len(rl.tailLog)-1, prevTerm)
	return terms
}

func (rl *RaftLog) last() (idx, term int) {
	return rl.size() - 1, rl.tailLog[len(rl.tailLog)-1].Term
}

func (rl *RaftLog) tail(startIdx int) []LogEntry {
	if startIdx >= rl.size() {
			return nil
	}
	return rl.tailLog[rl.idx(startIdx):]
}

func (rl *RaftLog) append(e LogEntry) {
	rl.tailLog = append(rl.tailLog, e)
}

func (rl *RaftLog) appendFrom(prevIdx int, entries []LogEntry) {
	rl.tailLog = append(rl.tailLog[:rl.idx(prevIdx)+1], entries...)
}

// more simplified
func (rl *RaftLog) Str() string {
	lastIdx, lastTerm := rl.last()
	return fmt.Sprintf("[%d]T%d~[%d]T%d", rl.snapLastIdx, rl.snapLastTerm, lastIdx, lastTerm)
}

// snapshot
// for application layer
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