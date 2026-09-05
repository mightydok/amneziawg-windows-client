# Network Configuration Quirks

As part of setting up a AmneziaWG tunnel, the tunnel service also sets up various network configuration parameters that are in one way or another related to the original configuration.

### Geo-split Routing (`GeoSplit = ru`)

When the `[Interface]` section contains `GeoSplit = <country code>`, the tunnel service routes that country's networks outside the tunnel while everything else keeps going through it. The `AllowedIPs` of the configuration are not changed, so `0.0.0.0/0` and `::/0` stay on the tunnel interface and the kill-switch semantics described below still apply. On top of that:

- The country's prefix list is loaded from `C:\Program Files\AmneziaWG\Data\geo\<cc>-v4.list` and `<cc>-v6.list`, or from the snapshot compiled into the binary when no downloaded list exists. The manager service refreshes the list from the configured sources (ipverse by default) before starting such a tunnel if the cached copy is older than the configured number of hours, waiting at most 15 seconds, and again in the background every few hours.
- A policy is applied to the list: IPv4 blocks smaller than the configured threshold (default `/22`) are sent through the tunnel instead, IPv6 can be routed by list or entirely through the tunnel, and the "always directly" and "always through the tunnel" prefixes are added or subtracted. Adjacent blocks are merged.
- For every remaining prefix a route is created on the interface that currently owns the physical default route, using that route's next hop and the marker protocol `RouteProtocolBbn` so leftovers of a crashed instance can be removed. Whenever the default route moves to another interface or next hop, for example when roaming between Wi-Fi and LTE, the routes are deleted and re-created. Until they are, traffic to those prefixes goes through the tunnel, never outside of it. Routes are removed when the tunnel stops.
- The kill-switch gets permit filters for the same prefixes (outbound connections only, batches of 256 prefixes per filter) at the weight of the tunnel interface, and, if enabled in the settings, a permit for private, link-local, CGNAT and multicast destinations placed above the DNS restriction so that LAN devices and other VPN adapters (including DNS servers they push) keep working.
- Outbound connections on adapters of other VPN clients are permitted as well, again above the DNS restriction: every adapter whose name or description contains one of the configured words (by default `TAP-Windows`, `OpenVPN`, `Wintun`, `WireGuard`; the tunnel's own adapter is always excluded) gets a permit when the kill-switch is enabled, and adapters that appear later are picked up from the interface change notification. Without this, a public address that another VPN pushes a route for, but which is not in the direct set, would be blocked by the kill-switch (`WSAEACCES`).
- DNS is not split by domain. Queries go to the servers of the `DNS =` line; whether they leave through the tunnel depends solely on whether the server's address is in the direct set.

Other VPN clients such as OpenVPN keep working alongside a geo-split tunnel because their routes are more specific than `/0`; a corporate profile that pushes `redirect-gateway` must filter it (`pull-filter ignore "redirect-gateway"`), otherwise its `/1` routes take over.

### Routing

The tunnel service takes all the allowed IPs from each peer, deduplicates them, and adds them to the routes for the AmneziaWG interface. The service then monitors which interface on the system has a default route (a route with a `/0` CIDR) that is not the AmneziaWG interface itself, and, if no MTU has been specified in the configuration, it sets the MTU of the AmneziaWG interface to be 80 less than the MTU of that default route interface. AmneziaWG also monitors the routing table and determines the outgoing route that does not loopback to itself, and then sends each packet using `IP_PKTINFO`/`IPV6_PKTINFO`. It keeps track of the incoming interface and source address for received packets, and always replies to the sender in that way.

### Firewall Considerations for `/0` Allowed IPs

If an interface has only one peer, and that peer contains an Allowed IP in `/0`, then AmneziaWG enables a so-called "kill-switch", which adds firewall rules to do the following:

- Packets from the tunnel service itself are permitted, so that AmneziaWG packets can flow successfully.
- If the configuration specifies DNS servers, then packets sent to port `53` are only permitted if they are to one of those DNS servers. This is to prevent Windows' [ordinary multihomed DNS resolution behavior](https://docs.microsoft.com/en-us/previous-versions/windows/it-pro/windows-server-2008-R2-and-2008/dd197552%28v%3Dws.10%29), so that DNS queries only go to the DNS server specified, rather than multiple DNS servers.
- Loopback packets are permitted, and packets actually going through the AmneziaWG tunnel are permitted.
- DHCP for IPv4 and IPv6 and NDP for IPv6 are permitted.
- All other packets are blocked.

This prevents traffic from leaking outside the tunnel.

If you'd like to use a default route _without_ having these restrictive kill-switch semantics, one may use the routes `0.0.0.0/1` and `128.0.0.0/1` in place of `0.0.0.0/0`, as well as `::/1` and `8000::/1` in place of `::/0`. This achieves nearly the same thing, but does not activate the above firewalling semantics. (The UI's editor has a checkbox that toggles this.)  And users without the need for a `/0` route at all do not have to worry about this, and instead fall back to ordinary Windows routing and DNS behavior.

### Considerations for non-`/0` Allowed IPs

When the above conditions do not apply, routing and DNS information is handed to Windows in the typical way for Windows to manage. This includes its [ordinary multihomed DNS resolution behavior](https://docs.microsoft.com/en-us/previous-versions/windows/it-pro/windows-server-2008-R2-and-2008/dd197552%28v%3Dws.10%29) as well as its ordinary routing table resolution. Users may make use of the normal Windows firewalling and network configuration capabilities to firewall this as needed. One firewall rule is added, however, which allows the tunnel service to send and receive AmneziaWG packets.

### Network List Manager

Windows assigns a unique GUID to each new AmneziaWG adapter. The application takes pains to make this GUID deterministic, so that firewall policy (such as "public" vs "private" network categorization) can be consistently applied to the tunnel's network. This determinism is based on the configuration of the tunnel. Therefore, if the AmneziaWG configuration changes, so too will the unique GUID. Technical details are described in [a mailing list post](https://lists.zx2c4.com/pipermail/wireguard/2019-June/004259.html).

### Adapter Lifetime

AmneziaWG's network adapter is created dynamically when a tunnel is started and destroyed when a tunnel is stopped. This means that additional filters, address families, or protocols should be bound to the adapter programmatically, possibly through use of dangerous script execution in the configuration file or by way of automatic NDIS layer binding.
