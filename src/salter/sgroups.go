// -------------------------------------------------------------------
//
// salter: Tool for bootstrap salt clusters in EC2
//
// Copyright (c) 2013 David Smith (dizzyd@dizzyd.com). All Rights Reserved.
//
// This file is provided to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file
// except in compliance with the License.  You may obtain
// a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.
//
// -------------------------------------------------------------------
package main

import "log"
import "fmt"
import "net"
import "strings"
import "strconv"
import "github.com/dizzyd/goamz/ec2"

type PermArray []ec2.IPPerm

// For each target node, ensure that the security group exists in the
// appropriate region and that the rules in local definition are present.
func sgroups() {
	// Setup a cache to track security groups we need to work on
	groups := make(map[string]*RegionalSGroup)

	// First, create any sgroups that need to exist
	for _, node := range G_CONFIG.Targets {
		sg, err := RegionSGEnsureExists(node.SGroup, node.RegionId)
		if err != nil {
			fmt.Printf("%s: Failed to create security group %s: %+v\n",
				node.Name, node.SGroup, err)
			return
		}
		groups[node.RegionId + "/" + node.SGroup] = sg
	}

	// Now, for each of the groups, make sure any and all locally-defined
	// rules are present
	for _, sg := range groups {
		// If the sg is not defined in our config, noop
		sgConf, found := G_CONFIG.SGroups[sg.Name]
		if !found {
			// Warn that this group was not found in local config
			fmt.Printf("%s: Security group %s is not defined in local config file.\n",
				sg.RegionId, sg.Name)
			continue
		}

		missingPerms := make([]ec2.IPPerm, 0)

		// Identify each rule that is not present on the security group
		for _, rule := range sgConf.Rules {
			perm, err := parseSGRule(rule, sg.RegionId)
			if err != nil {
				fmt.Printf("Invalid rule; %s\n", err)
				return
			}

			perms := PermArray(sg.IPPerms)
			if !perms.contains(*perm) {
				missingPerms = append(missingPerms, *perm)
			}
		}

		// Apply the missing perms to the security group
		if len(missingPerms) > 0 {
			fmt.Printf("Adding %d missing rules to %s-%s\n",
				len(missingPerms), sg.RegionId, sg.Name)
			log.Printf("Adding %d missing rules to %s-%s: %+v\n",
				len(missingPerms), sg.RegionId, sg.Name, missingPerms)
			region, _ := GetRegion(sg.RegionId)
			_, err := region.Conn.AuthorizeSecurityGroup(sg.SecurityGroup,
				missingPerms)
			if err != nil {
				fmt.Printf("Unable to add missing groups to %s: %+v\n%+v\n",
					sg.Name, err, missingPerms)
				return
			}
		}
	}
}


func isCidr(s string) bool {
	_, _, err := net.ParseCIDR(s)
	return err == nil
}

func setCidrOrGroup(part, rule, region string, perm *ec2.IPPerm) (*ec2.IPPerm, error) {
	if isCidr(part) {
		perm.SourceIPs = []string { part }
	} else {
		// Not a CIDR - lookup the corresponding security group
		if !RegionSGExists(part, region) {
			return nil, fmt.Errorf("Non-existent SG '%s' referenced in rule: %s",
				part, rule)
		}

		sg := RegionSG(part, region)
		usg := ec2.UserSecurityGroup {
			Id: sg.Id,
			Name: sg.Name,
			OwnerId: sg.OwnerId,
		}
		perm.SourceGroups = []ec2.UserSecurityGroup { usg }
	}
	return perm, nil
}

func parseSGRule(rule string, region string) (*ec2.IPPerm, error) {
	var perm ec2.IPPerm
	parts := strings.SplitN(rule, ":", 4)
	switch len(parts) {
	case 2:			// Proto:(IpCidr|GroupId)
		perm.Protocol = parts[0]
		return setCidrOrGroup(parts[1], rule, region, &perm)
	case 4:			// Proto:FromPort:ToPort:(IpCidr:GroupId)
		perm.Protocol = parts[0]
		perm.FromPort, _ = strconv.Atoi(parts[1])
		perm.ToPort, _ = strconv.Atoi(parts[2])
		return setCidrOrGroup(parts[3], rule, region, &perm)
	default:
		return nil, fmt.Errorf("Unknown rule format: %s", rule)
	}
}


func (perms PermArray) contains(perm ec2.IPPerm) bool {
	for _, p := range perms {
		fmt.Printf("I: %+v\nP: %+v\n", p, perm)
		if p.Protocol == perm.Protocol &&
			p.FromPort == perm.FromPort &&
			p.ToPort == perm.ToPort &&
			len(p.SourceIPs) == len(perm.SourceIPs) &&
			len(p.SourceGroups) == len(perm.SourceGroups) {
			for i := range p.SourceIPs {
				if p.SourceIPs[i] != perm.SourceIPs[i] {
					return false
				}
			}

			for i := range p.SourceGroups {
				if p.SourceGroups[i] != perm.SourceGroups[i] {
					return false
				}
			}
			return true
		}
	}
	return false
}

