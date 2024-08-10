package shardctrler

//
// Shardctrler clerk.
//

import (
	"crypto/rand"
	"fmt"
	"math/big"
	// "time"

	"6.5840/labrpc"
)

type Clerk struct {
	servers []*labrpc.ClientEnd
	// Your data here.
	leaderId int
	clientId int64 // unique id for each client
	seqId    int64
}

func nrand() int64 {
	max := big.NewInt(int64(1) << 62)
	bigx, _ := rand.Int(rand.Reader, max)
	x := bigx.Int64()
	return x
}

func MakeClerk(servers []*labrpc.ClientEnd) *Clerk {
	ck := new(Clerk)
	ck.servers = servers
	// Your code here.
	ck.leaderId = 0
	ck.clientId = nrand()
	ck.seqId = 0
	return ck
}

func (ck *Clerk) Query(num int) Config {
	args := &QueryArgs{}
	// Your code here.
	args.Num = num
	for {
		var reply QueryReply
		ok := ck.servers[ck.leaderId].Call("ShardCtrler.Query", args, &reply)
		// fmt.Printf("Query: %v\n", num)
		if !ok || reply.Err == ErrWrongLeader || reply.Err == ErrTimeout {
			ck.leaderId = (ck.leaderId + 1) % len(ck.servers)
			continue
		}
		// print the result for debugging
		fmt.Printf("Query: %v\n", reply.Config)
		return reply.Config
	}
}

func (ck *Clerk) Join(servers map[int][]string) {
	args := &JoinArgs{
		ClientId: ck.clientId,
		SeqId:   ck.seqId,
	}
	// Your code here.
	args.Servers = servers
	// print the result for debugging
	fmt.Printf("Join: %v\n", servers)

	for {
		var reply JoinReply
		fmt.Printf("Join: Sending request to server %d (LeaderId=%d)\n", ck.leaderId, ck.leaderId)
		ok := ck.servers[ck.leaderId].Call("ShardCtrler.Join", args, &reply)
		if !ok || reply.Err == ErrWrongLeader || reply.Err == ErrTimeout {
			ck.leaderId = (ck.leaderId + 1) % len(ck.servers)
			continue
		}
		// if !ok {
		// 	fmt.Printf("Join: Server %d did not respond, trying next server...\n", ck.leaderId)
		// } else if reply.Err == ErrWrongLeader {
		// 	fmt.Printf("Join: Server %d is not the leader, trying next server...\n", ck.leaderId)
		// } else if reply.Err == ErrTimeout {
		// 	fmt.Printf("Join: Request to server %d timed out, trying next server...\n", ck.leaderId)
		// } else {
		// 	// 成功
		// 	fmt.Printf("Join: Success, servers=%v, updated SeqId=%d\n", servers, ck.seqId+1)
		// 	ck.seqId++
		// 	return
		// }
		ck.seqId++
		// print a log for debugging
		// fmt.Printf("Join: %v\n", servers)
		// ck.leaderId = (ck.leaderId + 1) % len(ck.servers)
		fmt.Printf("Join: Switching to server %d (LeaderId=%d)\n", ck.leaderId, ck.leaderId)
	}
}

func (ck *Clerk) Leave(gids []int) {
	args := &LeaveArgs{
		ClientId: ck.clientId,
		SeqId:   ck.seqId,
	}
	// Your code here.
	args.GIDs = gids

	for {
		var reply LeaveReply
		ok := ck.servers[ck.leaderId].Call("ShardCtrler.Leave", args, &reply)
		if !ok || reply.Err == ErrWrongLeader || reply.Err == ErrTimeout {
			ck.leaderId = (ck.leaderId + 1) % len(ck.servers)
			continue
		}
		ck.seqId++
		// print a log for debugging
		fmt.Printf("Leave: %v\n", gids)
		return
	}
}

func (ck *Clerk) Move(shard int, gid int) {
	args := &MoveArgs{
		ClientId: ck.clientId,
		SeqId:   ck.seqId,
	}
	// Your code here.
	args.Shard = shard
	args.GID = gid

	for {
		var reply MoveReply
		ok := ck.servers[ck.leaderId].Call("ShardCtrler.Move", args, &reply)
		if !ok || reply.Err == ErrWrongLeader || reply.Err == ErrTimeout {
			ck.leaderId = (ck.leaderId + 1) % len(ck.servers)
			continue
		}
		ck.seqId++
		// print a log for debugging
		fmt.Printf("Move: %v %v\n", shard, gid)
		return
	}
}
