# PiFi

Ref: https://cdn-learn.adafruit.com/downloads/pdf/setting-up-a-raspberry-pi-as-a-wifi-access-point.pdf
https://thepi.io/how-to-use-your-raspberry-pi-as-a-wireless-access-point/

## Requirements

```
sudo apt install -y hostapd dnsmasq
sudo systemctl unmask hostapd
sudo systemctl enable hostapd
sudo DEBIAN_FRONTEND=noninteractive apt install -y netfilter-persistent iptables-persistent
```

Might need to reboot here.

Edit `/etc/dhcpcd.conf` and add the following lines to the end of the file:

```
interface wlan0
    static ip_address=192.168.4.1/24
    nohook wpa_supplicant
```

Edit `/etc/sysctl.d/routed-ap.conf` to enable routing - not sure this is needed for this use case:

```
net.ipv4.ip_forward=1
```

Edit `/etc/dnsmasq.conf` and add the following lines to the end of the file:

```
# Listening interface
interface=wlan0
# Pool of IP addresses served via DHCP
dhcp-range=192.168.4.2,192.168.4.20,255.255.255.0,24h
# Local wireless DNS domain
domain=wlan
# Alias for this router
address=/gw.wlan/192.168.4.1
```

Edit `/etc/hostapd/hostapd.conf` and add the following lines to the end of the file:

```
country_code=GB
interface=wlan0
ssid=WirelessNetworkName
hw_mode=g
channel=11
macaddr_acl=0
auth_algs=1
ignore_broadcast_ssid=0
wpa=2
wpa_passphrase=WirelessNetworkPassword
wpa_key_mgmt=WPA-PSK
wpa_pairwise=TKIP
rsn_pairwise=CCMP
```

Reset the wifi connection: `wpa_cli -i wlan0 reconfigure`

