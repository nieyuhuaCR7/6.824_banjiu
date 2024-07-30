package raft

// import "fmt"


func (rf *Raft) applicationTicker() {
	for rf.killed() == false {
		// Your code here (2B)
		rf.mu.Lock()
		rf.applyCond.Wait()
		
        entries := make([]LogEntry, 0)
		for i := rf.lastApplied + 1; i <= rf.commitIndex; i++ {
			entries = append(entries, rf.log[i])
		}
		rf.mu.Unlock()

		for i, entry := range entries {
			rf.applyCh <- ApplyMsg{
				CommandValid: entry.CommandValid,
				Command:      entry.Command,
				CommandIndex: rf.lastApplied + 1 + i,
			}
		}

		rf.mu.Lock()
		// fmt.Printf("Server %d applied %d entries\n", rf.me, len(entries))
		rf.lastApplied += len(entries)
		rf.mu.Unlock()
	}
}