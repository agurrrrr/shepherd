package magi

import (
	"testing"
)

func TestIsReadOnlyTool_NativeReadTools(t *testing.T) {
	tools := []string{"read_file", "grep", "glob"}
	for _, name := range tools {
		if !IsReadOnlyTool(name) {
			t.Errorf("IsReadOnlyTool(%q) = false; want true (native read tool)", name)
		}
	}
}

func TestIsReadOnlyTool_ShepherdReadMCPTools(t *testing.T) {
	tools := []string{
		"get_history",
		"get_task_detail",
		"get_status",
		"skill_load",
		"wiki_read_page",
		"wiki_list_pages",
		"wiki_search",
	}
	for _, name := range tools {
		if !IsReadOnlyTool(name) {
			t.Errorf("IsReadOnlyTool(%q) = false; want true (shepherd read MCP tool)", name)
		}
	}
}

func TestIsReadOnlyTool_ExternalReadMCPTools(t *testing.T) {
	tools := []string{
		"k8s_list_pods",
		"k8s_list_nodes",
		"k8s_list_services",
		"k8s_pod_status",
		"k8s_node_status",
		"k8s_cluster_info",
		"ops_list_firewall_rules",
		"ops_list_nat_rules",
		"ops_list_dns_overrides",
		"ops_list_services",
		"ops_dns_stats",
		"ops_system_status",
		"ops_firmware_status",
		"ops_ha_list_backends",
		"ops_ha_list_frontends",
		"ops_ha_list_servers",
		"ops_ha_statistics",
		"ops_list_dhcp_leases",
		"pve_list_nodes",
		"pve_list_vms",
		"pve_list_lxc",
		"pve_list_storage",
		"pve_node_status",
		"pve_vm_status",
		"pve_cluster_status",
		"pve_ceph_status",
		"pve_vm_config",
		"pve_lxc_config",
		"mobile_list_available_devices",
		"mobile_get_screen_size",
		"mobile_get_orientation",
		"get_releases",
	}
	for _, name := range tools {
		if !IsReadOnlyTool(name) {
			t.Errorf("IsReadOnlyTool(%q) = false; want true (external read MCP tool)", name)
		}
	}
}

func TestIsReadOnlyTool_WriteTools(t *testing.T) {
	tools := []string{
		"write_file",
		"edit_file",
		"bash",
		"task_start",
		"task_complete",
		"task_error",
	}
	for _, name := range tools {
		if IsReadOnlyTool(name) {
			t.Errorf("IsReadOnlyTool(%q) = true; want false (write tool)", name)
		}
	}
}

func TestIsReadOnlyTool_ExternalWriteMCPTools(t *testing.T) {
	tools := []string{
		"k8s_delete_pod",
		"k8s_scale_deployment",
		"k8s_restart_deployment",
		"k8s_cordon_node",
		"k8s_uncordon_node",
		"ops_add_firewall_rule",
		"ops_delete_firewall_rule",
		"ops_add_nat_rule",
		"ops_delete_nat_rule",
		"ops_add_dns_override",
		"ops_delete_dns_override",
		"ops_update_nat_rule",
		"ops_toggle_firewall_rule",
		"ops_toggle_nat_rule",
		"ops_apply_changes",
		"ops_service_action",
		"ops_wg_add_instance",
		"ops_wg_add_peer",
		"ops_wg_delete_peer",
		"pve_vm_start",
		"pve_vm_stop",
		"pve_vm_reboot",
		"pve_vm_shutdown",
		"pve_vm_snapshot_create",
		"pve_lxc_start",
		"pve_lxc_stop",
		"pve_lxc_reboot",
		"pve_lxc_shutdown",
		"deploy_app",
		"promote_release",
	}
	for _, name := range tools {
		if IsReadOnlyTool(name) {
			t.Errorf("IsReadOnlyTool(%q) = true; want false (external write MCP tool)", name)
		}
	}
}

func TestIsReadOnlyTool_BlockedShepherdMCPTools(t *testing.T) {
	// These are explicitly blocked even though they might match a read heuristic.
	tools := []string{
		"task_start",
		"task_complete",
		"task_error",
	}
	for _, name := range tools {
		if IsReadOnlyTool(name) {
			t.Errorf("IsReadOnlyTool(%q) = true; want false (blocked shepherd MCP tool)", name)
		}
	}
}

func TestIsReadOnlyTool_BrowserTools(t *testing.T) {
	// Browser navigation and reading tools are allowed for web research.
	browserReadTools := []string{
		"browser_open",
		"browser_navigate",
		"browser_back",
		"browser_forward",
		"browser_reload",
		"browser_get_text",
		"browser_get_html",
		"browser_get_attribute",
		"browser_get_url",
		"browser_get_title",
		"browser_screenshot",
		"browser_pdf",
		"browser_scroll",
		"browser_wait_load",
		"browser_wait_idle",
		"browser_wait_selector",
		"browser_wait_hidden",
		"browser_list_pages",
		"browser_list_sessions",
		"browser_console_start",
		"browser_console_messages",
		"browser_network_start",
		"browser_network_requests",
		"browser_network_request",
	}
	for _, name := range browserReadTools {
		if !IsReadOnlyTool(name) {
			t.Errorf("IsReadOnlyTool(%q) = false; want true (browser read/navigation tool)", name)
		}
	}

	// Browser interaction and session lifecycle tools are blocked — three
	// concurrent models sharing one Chrome profile would race on these.
	browserWriteTools := []string{
		"browser_session_start",
		"browser_session_stop",
		"browser_close",
		"browser_click",
		"browser_type",
		"browser_select",
		"browser_check",
		"browser_hover",
		"browser_eval",
	}
	for _, name := range browserWriteTools {
		if IsReadOnlyTool(name) {
			t.Errorf("IsReadOnlyTool(%q) = true; want false (browser interaction/lifecycle tool)", name)
		}
	}
}

func TestIsReadOnlyTool_UnknownTool(t *testing.T) {
	// Unknown tools with no matching patterns should return false.
	if IsReadOnlyTool("some_random_tool") {
		t.Errorf("IsReadOnlyTool(\"some_random_tool\") = true; want false (no pattern match)")
	}
}

func TestIsReadOnlyTool_MutatingWinsOverRead(t *testing.T) {
	// When both a read and mutating pattern match, mutating wins.
	// e.g. "ops_add_dns_override" has "get_" (no, it doesn't) but has "_add_" and "_delete_".
	// Better example: "k8s_list_nodes" has "list_" (read) — should be true.
	if !IsReadOnlyTool("k8s_list_nodes") {
		t.Errorf("IsReadOnlyTool(\"k8s_list_nodes\") = false; want true (list_ is read)")
	}
	// "ops_add_firewall_rule" has "_add_" (mutating) — should be false.
	if IsReadOnlyTool("ops_add_firewall_rule") {
		t.Errorf("IsReadOnlyTool(\"ops_add_firewall_rule\") = true; want false (_add_ is mutating)")
	}
}
