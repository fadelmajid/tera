# Keeping the server findable — TASKS 8.3

**R8.6: the server must be reachable by a stable address. DHCP reassignment must
not break access.**

The install story is "copy the binary, run it, everyone opens a URL"
(ARCHITECTURE §1). That URL is the weak point: the router hands the machine an
address, renews it for months, and then one day hands it a different one. Every
bookmark in the shop breaks at once, and the symptom — a browser that cannot
connect — looks exactly like the server being down.

## The answer is a name, not an address

Every desktop OS the shop might use already resolves `.local` names on the
local network: macOS and most Linux desktops run a responder for their own
hostname, and Windows has done so natively since Windows 10.

So the fix is to **set the server machine's hostname once**, and have everyone
bookmark that instead:

```
http://tera.local:8080
```

Tera prints this at every start, beside the IP addresses:

```
alamat tetap yang sebaiknya di-bookmark url=http://tera.local:8080
  catatan=alamat IP bisa berubah saat router membagi ulang; nama ini tidak
```

### Setting the hostname

| OS | How |
|---|---|
| Windows | Settings → System → About → Rename this PC → `tera` → restart |
| macOS | System Settings → General → Sharing → Local hostname → `tera` |
| Linux | `sudo hostnamectl set-hostname tera` |

### Why Tera does not ship its own mDNS responder

It would advertise a *service* (`_http._tcp`), which makes the server visible in
service browsers and does **not** make `tera.local` resolve. Resolving a
hostname needs a host responder, and every one of these machines already has
one. Shipping a second would add a dependency, duplicate the OS, and fix
nothing.

## Belt and braces: a DHCP reservation

Worth doing as well, because it also keeps the printer findable.

In the router's admin page, find the DHCP or LAN settings, and add a
**reservation** (sometimes "static lease" or "address binding") mapping the
server machine's MAC address to a fixed address. Do the same for the receipt
printer, which has the same problem and no name.

Do **not** set a static address on the machine itself without also reserving it
in the router: the router does not know the address is taken and will
eventually hand it to something else.

## When it moves anyway

Tera notices. It records the addresses it served on last time, beside the
database, and says so when they change:

```
alamat IP mesin ini berubah sejak terakhir dijalankan
  sebelumnya=192.168.1.10 sekarang=192.168.1.17
  akibat=bookmark di perangkat lain kemungkinan besar sudah tidak berlaku
  saran=pakai alamat .local, atau minta router menetapkan IP tetap untuk mesin ini
```

A warning, never a refusal. A till that will not start because its IP moved is
a worse outage than the one this is warning about.
