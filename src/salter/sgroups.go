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

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/mitchellh/goamz/ec2"
)

type PermArray []ec2.IPPerm

// For each target node, ensure that the security group exists in the
// appropriate region and that the rules in local definition are present.
func sgroups() error {
	// This is a cache of all the security groups that have been verified
	// to exist already.
	seen_groups := make(map[string]*RegionalSGroup)

	// This is a list of all groups that have been completely configured
	// already. This is used to ensure that we don't try to setup a group
	// twice if two nodes are in the same security group.
	setup_groups := make(map[string]bool)

	// Walk through each node that we are touching and setup the security
	// group attached to it (if we have not seen it already). Also create
	// security groups referenced by rules in this nodes config.
	for _, node := range G_TARGETS {
		sGroup := node.SGroup
		if _, exist := setup_groups[sGroup]; exist {
			// The group was setup in a prior iteration.
			continue
		}

		// Get the security group form out cache. If the group is not in the
		// cache then fetch it.
		sg, ok := seen_groups[sGroup]
		if !ok {
			// The sgroup is not in the cache, fetch it from AWS. This call
			// will create the group if it doesn't exist as well so after
			// this the group should be configured at least.
			var err error
			sg, err = RegionSGEnsureExists(node.SGroup, node.RegionId)
			if err != nil {
				fatalf("%s: Failed to create security group %s: %#v\n",
					node.Name, node.SGroup, err)
			} else {
				// Mark the group as having been checked.
				seen_groups[sGroup] = sg
			}
		}

		// Get the sgroup configuration that was added in the configuration
		// file. If its not defined in the file then warn the user and move
		// on.
		sgConf, found := G_CONFIG.SGroups[sGroup]
		if !found {
			// Warn that this group was not found in local config
			debugf("%s: Security group %s is not defined in the config file.\n",
				sg.RegionId, sg.Name)
			continue
		}

		// Identify each rule that is not present in the live security
		// group configuration in AWS. Each missing permission will get added
		// to the missingPerms array so it can be added later.
		missingPerms := make([]ec2.IPPerm, 0)
		existingPerms := PermArray(sg.IPPerms)
		for _, rule := range sgConf.Rules {
			perms, err := parseSGRule(rule, sg.RegionId)
			if err != nil {
				fatalf("%s: Invalid rule (%s); %s\n", node.Name, rule, err)
			}

			// Walk through each of the returned permissions.
			for _, perm := range perms {
				if !existingPerms.contains(*perm) {
					missingPerms = append(missingPerms, *perm)
				}
			}
		}

		// If any permissions are missing then we need to inform the user
		// and then add them.
		if len(missingPerms) > 0 {
			printf("Adding %d missing rules to %s-%s\n",
				len(missingPerms), sg.RegionId, sg.Name)

			// Start by getting the region object from the cache.
			region, err := GetRegion(sg.RegionId)
			if err != nil {
				fatalf("Failed to get the region data for %s: %#v",
					sg.RegionId, err)
			}

			// And now add the various rules to the security group in the
			// region.
			_, err = region.Conn.AuthorizeSecurityGroup(sg.SecurityGroup,
				missingPerms)
			if err != nil {
				fatalf("Unable to add missing rules for %s: %+v\n%+v\n",
					sg.Name, err, missingPerms)
			}
		}

		// Mark the sgroup as having been setup so that we don't attempt to
		// set it up again for another node in the same sgroup.
		setup_groups[sGroup] = true
	}

	return nil
}

// This function parses a string element from the sgroups section into
// a list of rules. This returns a list because a single line can turn
// into several rules via various expansion properties.
//
// Valid formats for security group rules are as follows:
// proto:match -> Allows all traffic using the given protocol to the matching
//     set of ip addresses.
// proto:port:match -> Allows traffic using the given protocol using the
//     specific port and coming form a matching ip address.
// proto:from_port:to_port:match -> Allows all traffic using the given
//     protocol that is destined to a port between (inclusive) of from_port
//     and to_port so long as it is from a matching ip address.
//
// 'proto' can be well known values such as 'tcp', 'udp', 'icmp' or it can
//     be a special value 'tcp/udp' which will automatically expand the rule
//     into two rules, one for tcp and one for udp.
// 'port' can be any number between 1 and 65535 (inclusive) for tcp and 0 and
//     255 for icmp.
// 'from_port' and 'to_port' are like port, except that to_port must always
//     be larger or equal to from_port, except if the protocol is icmp and
//     the to_port value is -1.
// 'match' is either a CIDR network address, or the name of a security group.
//     this may also be a special value of '*' which will expand the rule into
//     a series of rules that match all sgroups configured in the config file.
func parseSGRule(rule, region string) ([]*ec2.IPPerm, error) {
	// Checks a port string to see if it is within a proper range for
	// tcp/udp ports. This takes the fromPort and toPort strings and returns
	// an error if either does not make sense for a standard tcp or udp rule.
	// This returns the from/to ports as integers.
	checkNormalPort := func(from string, to string) (int, int, error) {
		// Start with the from_port value.
		fromInt, err := strconv.Atoi(from)
		if err != nil {
			return 0, 0, fmt.Errorf("(%s): from_port is not an integer: %s",
				rule, err)
		} else if fromInt < 1 {
			return 0, 0, fmt.Errorf("(%s): from_port is less than 1: %d",
				rule, fromInt)
		} else if fromInt > 65535 {
			return 0, 0, fmt.Errorf("(%s): from_port is larger than 65535: %d",
				rule, fromInt)
		}

		// Next parse the to_port value.
		if to == "" {
			return fromInt, fromInt, nil
		} else if toInt, err := strconv.Atoi(to); err != nil {
			return 0, 0, fmt.Errorf("(%s): to_port is not an integer: %s",
				rule, err)
		} else if toInt < fromInt {
			return 0, 0, fmt.Errorf(
				"(%s): to_port (%d) can not be less than from_port (%d)",
				rule, toInt, fromInt)
		} else if toInt > 65535 {
			return 0, 0, fmt.Errorf(
				"(%d): to_port can not be greater than 65535: %d", toInt)
		} else {
			return fromInt, toInt, nil
		}
	}

	// This function checks for ICMP protocol rules. With ICMP the ports
	// are yused a little differently as from_port is "type" and to_port
	// becomes "code". AWS does not support ranges with ICMP rules.
	checkICMPPort := func(t string, c string) (int, int, error) {
		// Parse the type and code strings into valid range values.
		// Note that ICMP rules do not work like tcp/udp rules so we need to
		// use -1 for matching.
		fromInt := -1
		toInt := -1

		// FIXME: Add support for more of the ICMP types here

		// Start by parsing the Type value (from_port)
		switch t {
		case "*":
			fromInt = -1
		case "echo_reply", "0":
			fromInt = 0
		case "echo_request", "ping", "8":
			fromInt = 8
		default:
			return 0, 0, fmt.Errorf("(%s): Unknown icmp type: %s", rule, t)
		}

		// Next we parse the Code value (to_port)
		switch c {
		case "*", "", "-1":
			toInt = -1
		default:
			return 0, 0, fmt.Errorf("(%s): Unknown code type: %s", rule, c)
		}

		// Success
		return fromInt, toInt, nil
	}

	// Checks that the protocol name is valid, and if so returns the function
	// that is used to check the ports for the protocol.
	checkProto := func(p string) (func(string, string) (int, int, error), error) {
		switch p {
		case "tcp":
			return checkNormalPort, nil
		case "udp":
			return checkNormalPort, nil
		case "tcp/udp":
			return checkNormalPort, nil
		case "icmp":
			return checkICMPPort, nil
		default:
			return nil, fmt.Errorf("(%s): Unknown protocol: %s", rule, p)
		}
	}

	// These values will be populated after the next step. We will expand out
	// the protocol and match sections after we ensure that the values all at
	// least seem sane.
	protocol := ""
	fromPort := 0
	toPort := 0
	match := ""

	// Split the rule string and set the various values depending on the
	// format of the line.
	parts := strings.SplitN(rule, ":", 4)
	switch len(parts) {
	case 2: // Proto:(IpCidr|GroupId)
		protocol = parts[0]
		match = parts[1]
		if f, err := checkProto(protocol); err != nil {
			return nil, err
		} else if fromPort, toPort, err = f("", ""); err != nil {
			return nil, err
		}
	case 3: // Proto:Port:(ipCidr|GroupId)
		protocol = parts[0]
		match = parts[2]
		if f, err := checkProto(protocol); err != nil {
			return nil, err
		} else if fromPort, toPort, err = f(parts[1], ""); err != nil {
			return nil, err
		}
	case 4: // Proto:FromPort:ToPort:(IpCidr:GroupId)
		protocol = parts[0]
		match = parts[3]
		if f, err := checkProto(protocol); err != nil {
			return nil, err
		} else if fromPort, toPort, err = f(parts[1], parts[2]); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("Unknown rule format: %s", rule)
	}

	// Next we need to make a list for returning all the exploded elements.
	ret := make([]*ec2.IPPerm, 0)

	// If match is '*' then we need to iterate the list of all sgroups defined
	// in the config file.
	matches := make(map[string]bool)
	if match == "*" {
		for _, node := range G_CONFIG.Nodes {
			matches[node.SGroup] = true
		}
	} else {
		matches[match] = true
	}

	// Walk through each of the matches adding them to the array of IPPerm
	// objects that need to be checked against.
	for match, _ := range matches {
		// Make a list of protocols that we are exploding into.
		protocols := []string{protocol}
		if protocol == "tcp/udp" {
			protocols = []string{"tcp", "udp"}
		}

		// Walk each protocol adding it to the ret list.
		for _, protocol := range protocols {
			perm := new(ec2.IPPerm)
			perm.Protocol = protocol
			perm.FromPort = fromPort
			perm.ToPort = toPort

			// See if match is a CIRD or a security group.
			if _, _, isCIDR := net.ParseCIDR(match); isCIDR == nil {
				perm.SourceIPs = []string{match}
			} else {
				// match is not a valid CIDR so we need to see if it is a
				// security group. Start by checking to see if the security
				// group exists so that we can warn the user that we are
				// creating it otherwise.
				if !RegionSGExists(match, region) {
					debugf("Creating security group: %s", match)
				}

				// Fetch the region from the cache or create it if
				// necessary.
				sg, err := RegionSGEnsureExists(match, region)
				if err != nil {
					fatalf("Unable to make %s: %#v", match, err)
				}

				// Setup the perm structure
				perm.SourceGroups = []ec2.UserSecurityGroup{
					ec2.UserSecurityGroup{
						Id:      sg.Id,
						Name:    sg.Name,
						OwnerId: sg.OwnerId,
					},
				}
			}

			// Add the permission to the list.
			ret = append(ret, perm)
		}
	}

	// Success!
	return ret, nil
}

func (perms PermArray) contains(perm ec2.IPPerm) bool {
	compareString := func(s1, s2 []string) bool {
		for _, d2 := range s2 {
			found := false
			for _, d1 := range s1 {
				if d2 == d1 {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}

	compareSG := func(s1, s2 []ec2.UserSecurityGroup) bool {
		for _, d2 := range s2 {
			found := false
			for _, d1 := range s1 {
				if d1.Id == d2.Id {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}

	for _, p := range perms {
		switch {
		case p.Protocol != perm.Protocol:
			continue
		case p.FromPort != perm.FromPort:
			continue
		case p.ToPort != perm.ToPort:
			continue
		case !compareString(p.SourceIPs, perm.SourceIPs):
			continue
		case !compareSG(p.SourceGroups, perm.SourceGroups):
			continue
		default:
			return true
		}
	}
	return false
}
