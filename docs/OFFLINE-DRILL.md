# Offline drill — TASKS 2.11

**INV-11: the system must function fully with no internet, on the shop LAN
alone.** "Offline" here means no internet, not no network. Their internet is
unreliable; their own router is not, and separating those two is what makes the
whole deployment model simple (REQUIREMENTS §10).

This is a **physical drill**. It is not passed by reading it, and it is not
passed by the automated tests — those cover a different half.

## What the automated tests already prove

`internal/transport/http/offline_test.go`:

- **`TestNothingReachesTheInternet`** walks the repository and fails on any
  reference to a CDN, a web font host, a cloud SDK, or a telemetry package. It
  reads the source rather than mocking the network, because the failure this
  guards against is someone adding a font CDN or a licence check eighteen months
  from now — a mock would not see that.
- **`TestSaleCompletesWithoutDNSOrOutboundNetwork`** replaces the process
  resolver with one that fails every lookup, then opens a till, rings a sale,
  and renders the receipt.
- **`TestServerBindsTheLAN`** asserts the default bind address is `0.0.0.0`, not
  loopback. Bound to localhost the software runs perfectly and nobody but the
  server machine can reach it, which to a cashier on a second device is
  indistinguishable from the network being down.

What no test can prove: that the printer prints, that the drawer opens, that the
shop's WiFi carries a browser on a phone at the back of the room, and that a
person who is not the developer can complete a sale while the internet is out.

## The drill

Run it before go-live, and again after any change to the server machine, the
router, or the printer.

### Setup

1. Server machine running `tera`, printer and cash drawer attached.
2. A second device — the tablet or phone the shop will actually use, not the
   developer's laptop.
3. At least one product with stock on hand, attributed to a named owner.
4. A till open, or the ability to open one.

### Steps

| # | Do this | Expected |
|---|---|---|
| 1 | Note the server's LAN address from its startup log (`tera siap alamat=http://192.168.x.x:8080`). | An address on the shop's subnet, not `127.0.0.1`. |
| 2 | **Unplug the internet.** Pull the WAN cable from the router, or disable its uplink. Leave the router itself powered. | Devices stay connected to the shop WiFi. |
| 3 | Confirm the internet is genuinely gone: on the second device, load any public website. | It fails. If it loads, you unplugged the wrong thing. |
| 4 | On the second device, open the server address in a browser. | The login screen appears. No spinner, no missing fonts, no blank page. |
| 5 | Log in as the cashier account. | Reaches the till. |
| 6 | Open a cash session with a float. | Accepted. |
| 7 | Scan a barcode with the physical scanner. | The item lands in the cart. |
| 8 | Add a second item by tapping the grid, set a quantity, take cash, and pay. | Sale saves; a nota number appears. |
| 9 | **Watch the printer.** | The receipt prints and cuts, and the drawer pops as it cuts. |
| 10 | Read the printed receipt. | Shop name, nota number, date, items, total, change. Columns line up; nothing is cut off the edge of the paper. |
| 11 | Void that sale (till is still open), and check the stock. | Stock returns to the exact quantity it was before. |
| 12 | Ring a second sale and close the session. | The Z-report's expected cash matches the drawer, and non-cash takings are listed but not counted into it. |
| 13 | **Plug the internet back in.** | Nothing changes. Nothing syncs, nothing catches up, because nothing was waiting. |

### Failure conditions

Any of these means the drill failed, not that it needs a retry:

- The browser shows an unstyled page, or a font takes a visible moment to
  appear — something is being fetched from outside the shop.
- The receipt prints but the drawer does not — check `TERA_DRAWER_PIN`; there
  are two common wirings and the driver defaults to `0`.
- The paper does not cut — set `TERA_PRINTER_PARTIAL_CUT=1` and try again. Some
  models implement only one of the two cut commands.
- Text runs off the edge of the paper — `TERA_PRINTER_WIDTH` is wrong. 32 for
  58mm paper, 48 for 80mm.
- The second device cannot reach the server — the server bound loopback, or the
  device is on a guest network isolated from the LAN.

### Printer configuration

Set on the **server machine**, since that is where the hardware is attached:

```
TERA_PRINTER_ADDR=192.168.1.50:9100   # network printer, or
TERA_PRINTER_DEVICE=/dev/usb/lp0      # USB printer
TERA_PRINTER_WIDTH=32                 # 32 for 58mm paper, 48 for 80mm
TERA_DRAWER_PIN=0                     # 0 or 1, whichever the cable uses
TERA_PRINTER_PARTIAL_CUT=0            # 1 if the paper does not cut
TERA_RECEIPT_FOOTER=Terima kasih
```

Unset means no printer. That is a supported state: the shop can trade and read
receipts on screen until the hardware arrives.

The driver sends only the portable ESC/POS subset — initialise, alignment,
emphasis, double-height, feed, cut, drawer pulse — which every printer sold as
"ESC/POS compatible" implements. Logo upload, barcode rendering, and code-page
switching vary by manufacturer and are deliberately not used.

**Not yet verified against real hardware.** The shop's printer model is still
unknown (see the open questions before Phase 2). Step 9 of this drill is the
first time this code will have driven a physical printer, and the failure
conditions above are the settings to reach for when it does not work first try.

## Record the result

Date, who ran it, which device, which printer, and any setting that had to be
changed. A drill nobody wrote down is a drill that gets argued about.
