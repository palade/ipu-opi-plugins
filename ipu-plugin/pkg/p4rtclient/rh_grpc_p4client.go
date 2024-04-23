package p4rtclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/intel/ipu-opi-plugins/ipu-plugin/pkg/utils"
	v1 "github.com/p4lang/p4runtime/go/p4/config/v1"
	p4api "github.com/p4lang/p4runtime/go/p4/v1"
	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var DEFAULT_DEVICE_ID uint64 = 1

type P4RTClient struct {
	client     p4api.P4RuntimeClient
	stream     p4api.P4Runtime_StreamChannelClient
	deviceId   uint64
	electionId *p4api.Uint128
	masterChan chan bool
	isMaster   bool
}

type P4Tuple struct {
	id    int
	value []byte
}

func NewP4RTClient(serverAddressPort string) *P4RTClient {
	conn, err := grpc.Dial(serverAddressPort, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to dial switch: %v", err)
	}
	p4client := &P4RTClient{
		client:   p4api.NewP4RuntimeClient(conn),
		deviceId: DEFAULT_DEVICE_ID,
	}
	Init(p4client)
	return p4client
}

func Init(c *P4RTClient) (err error) {
	c.masterChan = make(chan bool)
	c.stream, err = c.client.StreamChannel(context.Background())
	if err != nil {
		log.Fatalf("Error creating StreamChannel %v", err)
	}
	go func() {
		for {
			res, err := c.stream.Recv()
			if err != nil {
				log.Fatalf("stream recv error: %v", err)
			} else if arb := res.GetArbitration(); arb != nil {
				if code.Code(arb.Status.Code) == code.Code_OK {
					c.masterChan <- true
				} else {
					electionId := arb.ElectionId.Low
					newElectionId := &p4api.Uint128{
						Low:  electionId + uint64(1),
						High: arb.ElectionId.High,
					}
					go c.SetMasterArbitration(newElectionId)
				}
			} else {
				log.Printf("Message received")
			}
		}
	}()
	return
}

func (c *P4RTClient) SetMasterArbitration(electionId *p4api.Uint128) {
	c.electionId = electionId
	request := &p4api.StreamMessageRequest{
		Update: &p4api.StreamMessageRequest_Arbitration{
			Arbitration: &p4api.MasterArbitrationUpdate{
				DeviceId:   c.deviceId,
				ElectionId: electionId,
			},
		},
	}
	err := c.stream.Send(request)
	if err != nil {
		log.Fatalf("failed to set mastership %v, unable to proceed", err)
	}
}

func (c *P4RTClient) BecomeMaster() (bool, error) {
	tries := 10 // wait 10 seconds
	for !c.isMaster {
		c.SetMasterArbitration(&p4api.Uint128{High: 0, Low: 1})
		c.isMaster = <-c.masterChan
		time.Sleep(time.Millisecond * 1000)
		if tries == 0 {
			return false, fmt.Errorf("not receive mastership arbitration in time")
		} else {
			tries--
		}
	}
	return c.isMaster, nil
}

func (c *P4RTClient) GetForwardingPipelineConfig(request *p4api.GetForwardingPipelineConfigRequest, opts ...grpc.CallOption) (*p4api.GetForwardingPipelineConfigResponse, error) {
	log.Println("Received GetForwardingPipelineConfig request", request)
	resp, err := c.client.GetForwardingPipelineConfig(context.Background(), request, opts...)
	return resp, err
}

func (c *P4RTClient) Read(request *p4api.ReadRequest, opts ...grpc.CallOption) (p4api.P4Runtime_ReadClient, error) {
	log.Println("Received Read request", request)
	resp, err := c.client.Read(context.Background(), request, opts...)
	return resp, err
}

func (c *P4RTClient) Write(request *p4api.WriteRequest, opts ...grpc.CallOption) (*p4api.WriteResponse, error) {
	log.Println("Received Write request", request)
	resp, err := c.client.Write(context.Background(), request, opts...)
	return resp, err
}

func createP4Entity(p4Info *v1.P4Info, tableName, actionName string, fieldMatch []*P4Tuple, actionParams []*P4Tuple) (*p4api.Entity, error) {

	tableId, err := getTableId(p4Info, tableName)

	if err != nil {
		return nil, fmt.Errorf("unable to find table with name %s", tableName)
	}

	actionId, err := getActionId(p4Info, tableName, actionName)

	if err != nil {
		return nil, fmt.Errorf("unable to find action with name %s for table %s", actionName, tableName)
	}

	var p4FieldMatch []*p4api.FieldMatch

	for i := range fieldMatch {
		p4FieldMatch = append(p4FieldMatch,
			&p4api.FieldMatch{
				FieldId:        uint32(fieldMatch[i].id),
				FieldMatchType: &p4api.FieldMatch_Exact_{Exact: &p4api.FieldMatch_Exact{Value: fieldMatch[i].value}},
			},
		)
	}

	var p4ActionParams []*p4api.Action_Param

	for i := range actionParams {
		p4ActionParams = append(p4ActionParams,
			&p4api.Action_Param{ParamId: uint32(actionParams[i].id), Value: actionParams[i].value},
		)
	}

	entity := &p4api.Entity{
		Entity: &p4api.Entity_TableEntry{
			TableEntry: &p4api.TableEntry{
				TableId: tableId, Match: p4FieldMatch,
				Action: &p4api.TableAction{
					Type: &p4api.TableAction_Action{
						Action: &p4api.Action{ActionId: actionId, Params: p4ActionParams},
					},
				},
			},
		},
	}

	return entity, nil
}

func getP4Info(c *P4RTClient) (*v1.P4Info, error) {
	resp, err := c.GetForwardingPipelineConfig(&p4api.GetForwardingPipelineConfigRequest{
		DeviceId:     c.deviceId,
		ResponseType: p4api.GetForwardingPipelineConfigRequest_P4INFO_AND_COOKIE,
	})

	if err != nil {
		return nil, fmt.Errorf("error when retrieving forwarding pipeline config: %v", err)
	}

	config := resp.GetConfig()
	if config == nil {
		return nil, fmt.Errorf("error when retrieving config from forwarding pipeline: %v", err)
	}

	return config.GetP4Info(), nil
}

func getActionId(p4Info *v1.P4Info, tableName, actionName string) (uint32, error) {
	var id uint32

	for i := 0; i < len(p4Info.Tables); i++ {
		if p4Info.Tables[i].Preamble.Alias == tableName {
			for j := 0; j < len(p4Info.Tables[i].ActionRefs); j++ {
				for k := 0; k < len(p4Info.Actions); k++ {
					if p4Info.Tables[i].ActionRefs[j].Id == p4Info.Actions[k].Preamble.Id && p4Info.Actions[k].Preamble.Alias == actionName {
						id = p4Info.Actions[k].Preamble.Id
						break
					}
				}
			}
		}
	}

	return id, nil
}

func getTableId(p4Info *v1.P4Info, tableName string) (uint32, error) {

	var id uint32

	for i := 0; i < len(p4Info.Tables); i++ {

		if p4Info.Tables[i].Preamble.Alias == tableName {
			id = p4Info.Tables[i].Preamble.Id
			break
		}
	}

	return id, nil
}

func getTableEntities(c *P4RTClient, tableId uint32) ([]*p4api.Entity, error) {
	request := &p4api.ReadRequest{
		DeviceId: c.deviceId,
		Entities: []*p4api.Entity{
			{
				Entity: &p4api.Entity_TableEntry{TableEntry: &p4api.TableEntry{TableId: tableId}},
			},
		},
	}

	stream, err := c.Read(request)
	if err != nil {
		return nil, err
	}

	response, err2 := stream.Recv()
	if err2 != nil && !errors.Is(err2, io.EOF) {
		return nil, err2
	}

	return response.Entities, nil
}

func writeBatchRequest(c *P4RTClient, updates []*p4api.Update) error {
	req := &p4api.WriteRequest{
		DeviceId:   c.deviceId,
		ElectionId: c.electionId,
		Updates:    updates,
	}

	if _, err := c.Write(req); err != nil {
		return fmt.Errorf("unable to write batch request: %v", err)
	}

	return nil
}

func (c *P4RTClient) ClearAllTables() error {

	p4Info, err := getP4Info(c)
	if err != nil {
		return fmt.Errorf("error when retrieving P4Info from config: %v", err)
	}

	var updates []*p4api.Update

	for i := 0; i < len(p4Info.Tables); i++ {

		entities, err := getTableEntities(c, p4Info.Tables[i].Preamble.Id)

		if err != nil {
			return fmt.Errorf("error when retrieving entities for table %d: %v", p4Info.Tables[i].Preamble.Id, err)
		}

		for j := 0; j < len(entities); j++ {
			updates = append(updates, &p4api.Update{
				Type:   p4api.Update_DELETE,
				Entity: &p4api.Entity{Entity: entities[j].GetEntity()},
			})
		}
	}

	if err := writeBatchRequest(c, updates); err != nil {
		log.Fatalf("error when deleting tables: %v", err)
		return err
	}

	return nil
}

func AddRules(c *P4RTClient, macAddr []byte, vlan int, portMuxVsi int) {
	p4Info, err := getP4Info(c)

	if err != nil {
		fmt.Printf("error when retrieving P4Info from config: %v", err)
		return
	}

	var updates []*p4api.Update

	vfVport := utils.GetVportForVsi(int(macAddr[1]))
	portMuxVport := utils.GetVportForVsi(portMuxVsi)

	entity1, err := createP4Entity(p4Info, "ingress_loopback_table", "fwd_to_port",
		[]*P4Tuple{{1, []byte{0x00, byte(portMuxVsi)}}, {2, []byte{0x00, macAddr[1]}}},
		[]*P4Tuple{{1, []byte{0x00, 0x00, 0x00, byte(vfVport)}}},
	)

	if err != nil {
		fmt.Printf("unable to create P4 rule: %v", err)
		return
	}

	entity2, err := createP4Entity(p4Info, "portmux_egress_resp_dmac_vsi_table", "vlan_pop_ctag_stag",
		[]*P4Tuple{{1, macAddr}, {2, []byte{0x00, byte(portMuxVsi)}}},
		[]*P4Tuple{{1, []byte{0x00, 0x00, byte(portMuxModPtr)}}, {2, []byte{0x00, 0x00, 0x00, byte(vfVport)}}},
	)

	if err != nil {
		fmt.Printf("unable to create P4 rule: %v", err)
		return
	}

	entity3, err := createP4Entity(p4Info, "vport_arp_egress_table", "send_to_port_mux",
		[]*P4Tuple{{1, []byte{0x00, macAddr[1]}}, {2, []byte{0x00, 0x00, 0x00, 0x00}}},
		[]*P4Tuple{{1, []byte{0x00, 0x00, macAddr[1]}}, {2, []byte{0x00, 0x00, 0x00, byte(portMuxVport)}}},
	)

	if err != nil {
		fmt.Printf("unable to create P4 rule: %v", err)
		return
	}

	entity4, err := createP4Entity(p4Info, "vlan_push_ctag_stag_mod_table", "mod_vlan_push_ctag_stag",
		[]*P4Tuple{{1, []byte{0x00, 0x00, macAddr[1]}}},
		[]*P4Tuple{{1, []byte{0x01}}, {2, []byte{0x01}}, {3, []byte{0x00, byte(vlan)}}, {4, []byte{0x01}}, {5, []byte{0x01}}, {6, []byte{0x00, 0x00}}},
	)

	if err != nil {
		fmt.Printf("unable to create P4 rule: %v", err)
		return
	}

	entity5, err := createP4Entity(p4Info, "portmux_egress_req_table", "vlan_pop_ctag_stag",
		[]*P4Tuple{{1, []byte{0x00, byte(portMuxVsi)}}, {2, []byte{0x00, byte(vlan)}}},
		[]*P4Tuple{{1, []byte{0x00, 0x00, byte(portMuxModPtr)}}, {2, []byte{0x00, 0x00, 0x00, byte(vfVport)}}},
	)

	if err != nil {
		fmt.Printf("unable to create P4 rule: %v", err)
		return
	}

	entity6, err := createP4Entity(p4Info, "portmux_ingress_loopback_table", "fwd_to_port",
		[]*P4Tuple{{2, []byte{0x00, 0x00, 0x00, 0x00}}},
		[]*P4Tuple{{1, []byte{0x00, 0x00, 0x00, byte(portMuxVport)}}},
	)

	if err != nil {
		fmt.Printf("unable to create P4 rule: %v", err)
		return
	}

	entity7, err := createP4Entity(p4Info, "vlan_pop_ctag_stag_mod_table", "mod_vlan_pop_ctag_stag",
		[]*P4Tuple{{1, []byte{0x00, 0x00, byte(portMuxModPtr)}}}, []*P4Tuple{})

	if err != nil {
		fmt.Printf("unable to create P4 rule: %v", err)
		return
	}

	updates = append(updates,
		&p4api.Update{Type: p4api.Update_INSERT, Entity: entity1},
		&p4api.Update{Type: p4api.Update_INSERT, Entity: entity2},
		&p4api.Update{Type: p4api.Update_INSERT, Entity: entity3},
		&p4api.Update{Type: p4api.Update_INSERT, Entity: entity4},
		&p4api.Update{Type: p4api.Update_INSERT, Entity: entity5},
		&p4api.Update{Type: p4api.Update_INSERT, Entity: entity6},
		&p4api.Update{Type: p4api.Update_INSERT, Entity: entity7},
	)

	if err := writeBatchRequest(c, updates); err != nil {
		log.Fatalf("error when creating tables: %v", err)
		return
	}
}

func DeleteRules(c *P4RTClient, macAddr []byte, vlan int, portMuxVsi int) {
	p4Info, err := getP4Info(c)

	if err != nil {
		fmt.Printf("error when retrieving P4Info from config: %v", err)
		return
	}

	var updates []*p4api.Update

	entity1, err := createP4Entity(p4Info, "ingress_loopback_table", "fwd_to_port",
		[]*P4Tuple{{1, []byte{0x00, byte(portMuxVsi)}}, {2, []byte{0x00, macAddr[1]}}}, []*P4Tuple{})

	if err != nil {
		fmt.Printf("unable to create P4 rule: %v", err)
		return
	}

	entity2, err := createP4Entity(p4Info, "portmux_egress_resp_dmac_vsi_table", "vlan_pop_ctag_stag",
		[]*P4Tuple{{1, macAddr}, {2, []byte{0x00, byte(portMuxVsi)}}}, []*P4Tuple{})

	if err != nil {
		fmt.Printf("unable to create P4 rule: %v", err)
		return
	}

	entity3, err := createP4Entity(p4Info, "vport_arp_egress_table", "send_to_port_mux",
		[]*P4Tuple{{1, []byte{0x00, macAddr[1]}}, {2, []byte{0x00, 0x00, 0x00, 0x00}}}, []*P4Tuple{})

	if err != nil {
		fmt.Printf("unable to create P4 rule: %v", err)
		return
	}

	entity4, err := createP4Entity(p4Info, "vlan_push_ctag_stag_mod_table", "mod_vlan_push_ctag_stag",
		[]*P4Tuple{{1, []byte{0x00, 0x00, macAddr[1]}}}, []*P4Tuple{})

	if err != nil {
		fmt.Printf("unable to create P4 rule: %v", err)
		return
	}

	entity5, err := createP4Entity(p4Info, "portmux_egress_req_table", "vlan_pop_ctag_stag",
		[]*P4Tuple{{1, []byte{0x00, byte(portMuxVsi)}}, {2, []byte{0x00, byte(vlan)}}}, []*P4Tuple{})

	if err != nil {
		fmt.Printf("unable to create P4 rule: %v", err)
		return
	}

	// Common Rules: These are not deleted on each DeleteBridgePort call.
	// entity6, err := createP4Entity(p4Info, "portmux_ingress_loopback_table", "fwd_to_port",
	// 	[]*P4Tuple{{2, []byte{0x00, 0x00, 0x00, 0x00}}},
	// 	[]*P4Tuple{{1, []byte{0x00, 0x00, 0x00, byte(portMuxVport)}}},
	// )

	// if err != nil {
	// 	fmt.Printf("unable to create P4 rule: %v", err)
	// 	return
	// }

	// Common Rules: These are not deleted on each DeleteBridgePort call.
	// entity7, err := createP4Entity(p4Info, "vlan_pop_ctag_stag_mod_table", "mod_vlan_pop_ctag_stag",
	// 	[]*P4Tuple{{1, []byte{0x00, 0x00, byte(portMuxModPtr)}}}, []*P4Tuple{})

	// if err != nil {
	// 	fmt.Printf("unable to create P4 rule: %v", err)
	// 	return
	// }

	updates = append(updates,
		&p4api.Update{Type: p4api.Update_DELETE, Entity: entity1},
		&p4api.Update{Type: p4api.Update_DELETE, Entity: entity2},
		&p4api.Update{Type: p4api.Update_DELETE, Entity: entity3},
		&p4api.Update{Type: p4api.Update_DELETE, Entity: entity4},
		&p4api.Update{Type: p4api.Update_DELETE, Entity: entity5},
		//&p4api.Update{Type: p4api.Update_DELETE, Entity: entity6}, 	// Common Rules: These are not deleted on each DeleteBridgePort call.
		//&p4api.Update{Type: p4api.Update_DELETE, Entity: entity7},	// Common Rules: These are not deleted on each DeleteBridgePort call.
	)

	if err := writeBatchRequest(c, updates); err != nil {
		log.Fatalf("error when creating tables: %v", err)
		return
	}
}

func (c *P4RTClient) CreateNetworkFunctionRules(vfMacList []string, apf1 string, apf2 string) {

	p4Info, err := getP4Info(c)

	if err != nil {
		fmt.Printf("error when retrieving P4Info from config: %v", err)
		return
	}

	var updates []*p4api.Update

	for i := range vfMacList {

		vfMac, err := utils.GetMacAsByteArray(vfMacList[i])
		if err != nil {
			fmt.Printf("unable to extract octets from %s: %v", vfMacList[i], err)
			return
		}

		apf1Mac, err := utils.GetMacAsByteArray(apf1)
		if err != nil {
			fmt.Printf("unable to extract octets from apf %s: %v", apf1, err)
			return
		}

		entity1, err := createP4Entity(p4Info, "ingress_loopback_table", "fwd_to_port",
			[]*P4Tuple{{1, []byte{0x00, vfMac[1]}}, {2, []byte{0x00, apf1Mac[1]}}},
			[]*P4Tuple{{1, []byte{0x00, 0x00, 0x00, apf1Mac[1] + 16}}},
		)

		if err != nil {
			fmt.Printf("unable to create P4 rule: %v", err)
			return
		}

		entity2, err := createP4Entity(p4Info, "ingress_loopback_table", "fwd_to_port",
			[]*P4Tuple{{1, []byte{0x00, apf1Mac[1]}}, {2, []byte{0x00, vfMac[1]}}},
			[]*P4Tuple{{1, []byte{0x00, 0x00, 0x00, vfMac[1] + 16}}},
		)

		if err != nil {
			fmt.Printf("unable to create P4 rule: %v", err)
			return
		}

		entity3, err := createP4Entity(p4Info, "vport_egress_vsi_table", "fwd_to_port",
			[]*P4Tuple{{1, []byte{0x00, vfMac[1]}}, {2, []byte{0x00, 0x00, 0x00, 0x00}}},
			[]*P4Tuple{{1, []byte{0x00, 0x00, 0x00, apf1Mac[1] + 16}}},
		)

		if err != nil {
			fmt.Printf("unable to create P4 rule: %v", err)
			return
		}

		entity4, err := createP4Entity(p4Info, "vport_egress_dmac_vsi_table", "fwd_to_port",
			[]*P4Tuple{{1, vfMac}, {2, []byte{0x00, apf1Mac[1]}}},
			[]*P4Tuple{{1, []byte{0x00, 0x00, 0x00, vfMac[1] + 16}}},
		)

		if err != nil {
			fmt.Printf("unable to create P4 rule: %v", err)
			return
		}

		updates = append(updates,
			&p4api.Update{Type: p4api.Update_INSERT, Entity: entity1},
			&p4api.Update{Type: p4api.Update_INSERT, Entity: entity2},
			&p4api.Update{Type: p4api.Update_INSERT, Entity: entity3},
			&p4api.Update{Type: p4api.Update_INSERT, Entity: entity4},
		)

	}

	apf2Mac, err := utils.GetMacAsByteArray(apf2)
	if err != nil {
		fmt.Printf("unable to extract octets from apf %s: %v", apf2, err)
		return
	}

	entity1, err := createP4Entity(p4Info, "ingress_loopback_table", "fwd_to_port",
		[]*P4Tuple{{1, []byte{0x00, apf2Mac[1]}}, {2, []byte{0x00, apf2Mac[1]}}},
		[]*P4Tuple{{1, []byte{0x00, 0x00, 0x00, apf2Mac[1] + 16}}},
	)

	if err != nil {
		fmt.Printf("unable to create P4 rule: %v", err)
		return
	}

	entity2, err := createP4Entity(p4Info, "vport_egress_vsi_table", "fwd_to_port",
		[]*P4Tuple{{1, []byte{0x00, apf2Mac[1]}}, {2, []byte{0x00, 0x00, 0x00, 0x00}}},
		[]*P4Tuple{{1, []byte{0x00, 0x00, 0x00, apf2Mac[1] + 16}}},
	)

	if err != nil {
		fmt.Printf("unable to create P4 rule: %v", err)
		return
	}

	updates = append(updates,
		&p4api.Update{Type: p4api.Update_INSERT, Entity: entity1},
		&p4api.Update{Type: p4api.Update_INSERT, Entity: entity2},
	)

	if err := writeBatchRequest(c, updates); err != nil {
		log.Fatalf("error when creating tables: %v", err)
		return
	}
}

func (c *P4RTClient) DeleteNetworkFunctionRules(vfMacList []string, apf1 string, apf2 string) {

	p4Info, err := getP4Info(c)

	if err != nil {
		fmt.Printf("error when retrieving P4Info from config: %v", err)
		return
	}

	var updates []*p4api.Update

	for i := range vfMacList {

		vfMac, err := utils.GetMacAsByteArray(vfMacList[i])
		if err != nil {
			fmt.Printf("unable to extract octets from %s: %v", vfMacList[i], err)
			return
		}

		apf1Mac, err := utils.GetMacAsByteArray(apf1)
		if err != nil {
			fmt.Printf("unable to extract octets from apf %s: %v", apf1, err)
			return
		}

		entity1, err := createP4Entity(p4Info, "ingress_loopback_table", "fwd_to_port",
			[]*P4Tuple{{1, []byte{0x00, vfMac[1]}}, {2, []byte{0x00, apf1Mac[1]}}},
			[]*P4Tuple{{1, []byte{}}},
		)

		if err != nil {
			fmt.Printf("unable to create P4 rule: %v", err)
			return
		}

		entity2, err := createP4Entity(p4Info, "ingress_loopback_table", "fwd_to_port",
			[]*P4Tuple{{1, []byte{0x00, apf1Mac[1]}}, {2, []byte{0x00, vfMac[1]}}},
			[]*P4Tuple{{1, []byte{}}},
		)

		if err != nil {
			fmt.Printf("unable to create P4 rule: %v", err)
			return
		}

		entity3, err := createP4Entity(p4Info, "vport_egress_vsi_table", "fwd_to_port",
			[]*P4Tuple{{1, []byte{0x00, vfMac[1]}}, {2, []byte{0x00, 0x00, 0x00, 0x00}}},
			[]*P4Tuple{{1, []byte{}}},
		)

		if err != nil {
			fmt.Printf("unable to create P4 rule: %v", err)
			return
		}

		entity4, err := createP4Entity(p4Info, "vport_egress_dmac_vsi_table", "fwd_to_port",
			[]*P4Tuple{{1, vfMac}, {2, []byte{0x00, apf1Mac[1]}}},
			[]*P4Tuple{{1, []byte{}}},
		)

		if err != nil {
			fmt.Printf("unable to create P4 rule: %v", err)
			return
		}

		updates = append(updates,
			&p4api.Update{Type: p4api.Update_DELETE, Entity: entity1},
			&p4api.Update{Type: p4api.Update_DELETE, Entity: entity2},
			&p4api.Update{Type: p4api.Update_DELETE, Entity: entity3},
			&p4api.Update{Type: p4api.Update_DELETE, Entity: entity4},
		)
	}

	apf2Mac, err := utils.GetMacAsByteArray(apf2)
	if err != nil {
		fmt.Printf("unable to extract octets from apf %s: %v", apf2, err)
		return
	}

	entity1, err := createP4Entity(p4Info, "ingress_loopback_table", "fwd_to_port",
		[]*P4Tuple{{1, []byte{0x00, apf2Mac[1]}}, {2, []byte{0x00, apf2Mac[1]}}},
		[]*P4Tuple{{1, []byte{}}},
	)

	if err != nil {
		fmt.Printf("unable to create P4 rule: %v", err)
		return
	}

	entity2, err := createP4Entity(p4Info, "vport_egress_vsi_table", "fwd_to_port",
		[]*P4Tuple{{1, []byte{0x00, apf2Mac[1]}}, {2, []byte{0x00, 0x00, 0x00, 0x00}}},
		[]*P4Tuple{{1, []byte{}}},
	)

	if err != nil {
		fmt.Printf("unable to create P4 rule: %v", err)
		return
	}

	updates = append(updates,
		&p4api.Update{Type: p4api.Update_DELETE, Entity: entity1},
		&p4api.Update{Type: p4api.Update_DELETE, Entity: entity2},
	)

	if err := writeBatchRequest(c, updates); err != nil {
		log.Fatalf("error when deleting tables: %v", err)
		return
	}
}

func (c *P4RTClient) CreatePointToPointVFRules(vfMacList []string) {
	p4Info, err := getP4Info(c)

	if err != nil {
		fmt.Printf("error when retrieving P4Info from config: %v", err)
		return
	}

	var updates []*p4api.Update

	for i := range vfMacList {
		for j := range vfMacList {
			if i != j {

				srcVfMac, err := utils.GetMacAsByteArray(vfMacList[i])
				if err != nil {
					fmt.Printf("unable to extract octets from %s: %v", vfMacList[i], err)
					return
				}

				dstVfMac, err := utils.GetMacAsByteArray(vfMacList[j])
				if err != nil {
					fmt.Printf("unable to extract octets from %s: %v", vfMacList[j], err)
					return
				}

				entity1, err := createP4Entity(p4Info, "ingress_loopback_table", "fwd_to_port",
					[]*P4Tuple{{1, []byte{0x00, srcVfMac[1]}}, {2, []byte{0x00, dstVfMac[1]}}},
					[]*P4Tuple{{1, []byte{0x00, 0x00, 0x00, dstVfMac[1] + 16}}},
				)

				if err != nil {
					fmt.Printf("unable to create P4 rule: %v", err)
					return
				}

				entity2, err := createP4Entity(p4Info, "vport_egress_dmac_vsi_table", "fwd_to_port",
					[]*P4Tuple{{1, dstVfMac}, {2, []byte{0x00, srcVfMac[1]}}},
					[]*P4Tuple{{1, []byte{0x00, 0x00, 0x00, dstVfMac[1] + 16}}},
				)

				if err != nil {
					fmt.Printf("unable to create P4 rule: %v", err)
					return
				}

				updates = append(updates,
					&p4api.Update{Type: p4api.Update_INSERT, Entity: entity1},
					&p4api.Update{Type: p4api.Update_INSERT, Entity: entity2},
				)
			}
		}
	}

	if err := writeBatchRequest(c, updates); err != nil {
		log.Fatalf("error when creating tables: %v", err)
		return
	}
}

func (c *P4RTClient) DeletePointToPointVFRules(vfMacList []string) {
	p4Info, err := getP4Info(c)

	if err != nil {
		fmt.Printf("error when retrieving P4Info from config: %v", err)
		return
	}

	var updates []*p4api.Update

	for i := range vfMacList {
		for j := range vfMacList {
			if i != j {

				srcVfMac, err := utils.GetMacAsByteArray(vfMacList[i])
				if err != nil {
					fmt.Printf("unable to extract octets from %s: %v", vfMacList[i], err)
					return
				}

				dstVfMac, err := utils.GetMacAsByteArray(vfMacList[j])
				if err != nil {
					fmt.Printf("unable to extract octets from %s: %v", vfMacList[j], err)
					return
				}

				entity1, err := createP4Entity(p4Info, "ingress_loopback_table", "fwd_to_port",
					[]*P4Tuple{{1, []byte{0x00, srcVfMac[1]}}, {2, []byte{0x00, dstVfMac[1]}}}, []*P4Tuple{})

				if err != nil {
					fmt.Printf("unable to create P4 rule: %v", err)
					return
				}

				entity2, err := createP4Entity(p4Info, "vport_egress_dmac_vsi_table", "fwd_to_port",
					[]*P4Tuple{{1, dstVfMac}, {2, []byte{0x00, srcVfMac[1]}}}, []*P4Tuple{})

				if err != nil {
					fmt.Printf("unable to create P4 rule: %v", err)
					return
				}

				updates = append(updates,
					&p4api.Update{Type: p4api.Update_DELETE, Entity: entity1},
					&p4api.Update{Type: p4api.Update_DELETE, Entity: entity2},
				)
			}
		}
	}

	if err := writeBatchRequest(c, updates); err != nil {
		log.Fatalf("error when deleting tables: %v", err)
		return
	}
}
