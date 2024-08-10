package shardctrler

import (
	"fmt"
	"sort"
)

type CtrlerStateMachine struct {
	Configs []Config // indexed by config num
}

func NewCtrlerStateMachine() *CtrlerStateMachine {
	cf := &CtrlerStateMachine{Configs: make([]Config, 1)}
	cf.Configs[0] = DefaultConfig()
	return cf
}

func (csm *CtrlerStateMachine) Query(num int) (Config, Err) {
	if num < 0 || num >= len(csm.Configs) {
		return csm.Configs[len(csm.Configs)-1], OK
	}
	return csm.Configs[num], OK
}

// add new groups into the system, may need to rebalance the shards
func (csm *CtrlerStateMachine) Join(groups map[int][]string) Err {
	fmt.Printf("State machine start: join %v\n", groups)
	num := len(csm.Configs)
	lastConfig := csm.Configs[num-1]
	// construct a new config
	newConfig := Config{
		Num:    num,
		Shards: lastConfig.Shards,
		Groups: copyGroups(lastConfig.Groups),
	}

	// add new groups into the new config
	for gid, servers := range groups {
		if _, ok := newConfig.Groups[gid]; !ok {
			newServers := make([]string, len(servers))
			copy(newServers, servers)
			newConfig.Groups[gid] = newServers
		}
	}

	// construct a mapping from gid to shards
	gidToShards := make(map[int][]int)
	for gid := range newConfig.Groups {
		gidToShards[gid] = make([]int, 0)
	}
	for shard, gid := range newConfig.Shards {
		gidToShards[gid] = append(gidToShards[gid], shard)
	}
  
	// rebalance the shards
	for {
		maxGid := gidWithMaxShards(gidToShards)
		minGid := gidWithMinShards(gidToShards)
		if maxGid != 0 && len(gidToShards[maxGid]) - len(gidToShards[minGid]) <= 1 {
			break
		}

		// move a shard from maxGid to minGid
		gidToShards[minGid] = append(gidToShards[minGid], gidToShards[maxGid][0])
		gidToShards[maxGid] = gidToShards[maxGid][1:]
	}

    // now we got the new shard allocation, update the new config
	var newShards [NShards]int
	for gid, shards := range gidToShards {
		for _, shard := range shards {
			newShards[shard] = gid
		}
	}
	newConfig.Shards = newShards
	csm.Configs = append(csm.Configs, newConfig)

	fmt.Printf("State machine: new config %v\n", newConfig)
	return OK
}

func (csm *CtrlerStateMachine) Leave(gids []int) Err {
	num := len(csm.Configs)
	lastConfig := csm.Configs[num-1]
	// construct a new config
	newConfig := Config{
		Num:    num,
		Shards: lastConfig.Shards,
		Groups: copyGroups(lastConfig.Groups),
	}

	// construct a mapping from gid to shards
	gidToShards := make(map[int][]int)
	for gid := range newConfig.Groups {
		gidToShards[gid] = make([]int, 0)
	}
	for shard, gid := range newConfig.Shards {
		gidToShards[gid] = append(gidToShards[gid], shard)
	}

	// delete the given groups and store the shards
	var unassignedShards []int
	for _, gid := range gids {
		// if this gid is in the new config, delete it
		if _, ok := newConfig.Groups[gid]; ok {
			delete(newConfig.Groups, gid)
		}
		// retrieve the shards of this gid
		if shards, ok := gidToShards[gid]; ok {
			unassignedShards = append(unassignedShards, shards...)
			delete(gidToShards, gid)
		}
	}

	var newShards [NShards]int
	// redistribute the unassigned shards
	if len(unassignedShards) > 0 {
		sort.Ints(unassignedShards)
		for _, shard := range unassignedShards {
			minGid := gidWithMinShards(gidToShards)
			gidToShards[minGid] = append(gidToShards[minGid], shard)
		}
	
		// now we got the new shard allocation, update the new config
		for gid, shards := range gidToShards {
			for _, shard := range shards {
				newShards[shard] = gid
			}
		}
	}

	newConfig.Shards = newShards
	csm.Configs = append(csm.Configs, newConfig)
    return OK
}

func (csm *CtrlerStateMachine) Move(shard int, gid int) Err {
	num := len(csm.Configs)
	lastConfig := csm.Configs[num-1]
	// construct a new config
	newConfig := Config{
		Num:    num,
		Shards: lastConfig.Shards,
		Groups: copyGroups(lastConfig.Groups),
	}

	// update the shard allocation
	newConfig.Shards[shard] = gid
	csm.Configs = append(csm.Configs, newConfig)
	return OK
}

func copyGroups(groups map[int][]string) map[int][]string {
	newGroups := make(map[int][]string)
	for k, v := range groups {
		newGroups[k] = make([]string, len(v))
		copy(newGroups[k], v)
	}
	return newGroups
}

func gidWithMaxShards(gidToShards map[int][]int) int {
	if shard, ok := gidToShards[0]; ok && len(shard) > 0 {
		return 0
	}
	
	var gids []int
	for gid := range gidToShards {
		gids = append(gids, gid)
	}
	sort.Ints(gids)

	maxGid, maxShards := -1, -1
	for _, gid := range gids {
		if len(gidToShards[gid]) > maxShards {
			maxGid = gid
			maxShards = len(gidToShards[gid])
		}
	}
	return maxGid
}

func gidWithMinShards(gidToShards map[int][]int) int {
	if shard, ok := gidToShards[0]; ok && len(shard) > 0 {
		return 0
	}

	var gids []int
	for gid := range gidToShards {
		gids = append(gids, gid)
	}
	sort.Ints(gids)

	minGid, minShards := -1, NShards+1
	for _, gid := range gids {
		if gid != 0 && len(gidToShards[gid]) < minShards {
			minGid = gid
			minShards = len(gidToShards[gid])
		}
	}
	return minGid
}