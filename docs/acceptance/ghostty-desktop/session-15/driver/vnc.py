#!/usr/bin/env python3
"""A headless VNC client: real keys, pointer, and pictures on another Mac.

Nothing is shown on the machine this runs on. Input sent this way is the
remote Mac's own screen-sharing input: modifier keys are pressed and released
as keys, which is what an app sees from a keyboard, and a picture is the
remote framebuffer, so neither needs an accessibility or screen-recording
grant there.

usage: vnc.py COMMAND [ARG...] [-- COMMAND [ARG...]]...
  size                         prints the framebuffer size
  key CHORD...                 presses chords such as shift+Page_Up, cmd+f, Return
  type TEXT                    types text, one key at a time
  typefile PATH                types a file's contents (trailing newline dropped)
                               without echoing them: for a password field
  move X Y
  click X Y [MODS]             left click, MODS like cmd+shift held as real keys
  dclick X Y
  rclick X Y
  drag X1 Y1 X2 Y2 [MODS]
  scroll X Y LINES             positive scrolls toward history (wheel up)
  shot FILE.png [X Y W H]      saves the framebuffer or a region of it
  sleep SECONDS
  daemon SOCKET                holds one connection open and serves the commands
                               sent to SOCKET; with AMX_VNC_SOCKET set, every
                               other invocation goes through it. macOS locks
                               the screen when a viewer disconnects, so a run
                               keeps one viewer throughout. `quit` ends it.

Coordinates are framebuffer pixels. Every run releases the modifiers and the
buttons it pressed, also when it fails midway.

Environment:
  AMX_VNC_HOST            host (default: the host of AMX_MAC_TEST_HOST)
  AMX_VNC_USER            a user of that Mac: authenticate as that user (Apple's
                          type 30) and join the desktop they are logged in to.
                          Unset: the shared VNC password, which gets a login
                          session of its own.
  AMX_VNC_PASSWORD_FILE   file holding that user's password, or the VNC
                          password; it is read here and never printed,
                          logged, or passed to a program
"""
import os
import select
import socket
import struct
import sys
import time
import zlib

# --- DES, for VNC authentication only ----------------------------------------
_PC1 = [57, 49, 41, 33, 25, 17, 9, 1, 58, 50, 42, 34, 26, 18, 10, 2, 59, 51, 43, 35, 27, 19, 11, 3, 60, 52, 44, 36,
        63, 55, 47, 39, 31, 23, 15, 7, 62, 54, 46, 38, 30, 22, 14, 6, 61, 53, 45, 37, 29, 21, 13, 5, 28, 20, 12, 4]
_PC2 = [14, 17, 11, 24, 1, 5, 3, 28, 15, 6, 21, 10, 23, 19, 12, 4, 26, 8, 16, 7, 27, 20, 13, 2,
        41, 52, 31, 37, 47, 55, 30, 40, 51, 45, 33, 48, 44, 49, 39, 56, 34, 53, 46, 42, 50, 36, 29, 32]
_IP = [58, 50, 42, 34, 26, 18, 10, 2, 60, 52, 44, 36, 28, 20, 12, 4, 62, 54, 46, 38, 30, 22, 14, 6, 64, 56, 48, 40, 32, 24, 16, 8,
       57, 49, 41, 33, 25, 17, 9, 1, 59, 51, 43, 35, 27, 19, 11, 3, 61, 53, 45, 37, 29, 21, 13, 5, 63, 55, 47, 39, 31, 23, 15, 7]
_FP = [40, 8, 48, 16, 56, 24, 64, 32, 39, 7, 47, 15, 55, 23, 63, 31, 38, 6, 46, 14, 54, 22, 62, 30, 37, 5, 45, 13, 53, 21, 61, 29,
       36, 4, 44, 12, 52, 20, 60, 28, 35, 3, 43, 11, 51, 19, 59, 27, 34, 2, 42, 10, 50, 18, 58, 26, 33, 1, 41, 9, 49, 17, 57, 25]
_E = [32, 1, 2, 3, 4, 5, 4, 5, 6, 7, 8, 9, 8, 9, 10, 11, 12, 13, 12, 13, 14, 15, 16, 17,
      16, 17, 18, 19, 20, 21, 20, 21, 22, 23, 24, 25, 24, 25, 26, 27, 28, 29, 28, 29, 30, 31, 32, 1]
_P = [16, 7, 20, 21, 29, 12, 28, 17, 1, 15, 23, 26, 5, 18, 31, 10, 2, 8, 24, 14, 32, 27, 3, 9, 19, 13, 30, 6, 22, 11, 4, 25]
_S = [
    [14, 4, 13, 1, 2, 15, 11, 8, 3, 10, 6, 12, 5, 9, 0, 7, 0, 15, 7, 4, 14, 2, 13, 1, 10, 6, 12, 11, 9, 5, 3, 8,
     4, 1, 14, 8, 13, 6, 2, 11, 15, 12, 9, 7, 3, 10, 5, 0, 15, 12, 8, 2, 4, 9, 1, 7, 5, 11, 3, 14, 10, 0, 6, 13],
    [15, 1, 8, 14, 6, 11, 3, 4, 9, 7, 2, 13, 12, 0, 5, 10, 3, 13, 4, 7, 15, 2, 8, 14, 12, 0, 1, 10, 6, 9, 11, 5,
     0, 14, 7, 11, 10, 4, 13, 1, 5, 8, 12, 6, 9, 3, 2, 15, 13, 8, 10, 1, 3, 15, 4, 2, 11, 6, 7, 12, 0, 5, 14, 9],
    [10, 0, 9, 14, 6, 3, 15, 5, 1, 13, 12, 7, 11, 4, 2, 8, 13, 7, 0, 9, 3, 4, 6, 10, 2, 8, 5, 14, 12, 11, 15, 1,
     13, 6, 4, 9, 8, 15, 3, 0, 11, 1, 2, 12, 5, 10, 14, 7, 1, 10, 13, 0, 6, 9, 8, 7, 4, 15, 14, 3, 11, 5, 2, 12],
    [7, 13, 14, 3, 0, 6, 9, 10, 1, 2, 8, 5, 11, 12, 4, 15, 13, 8, 11, 5, 6, 15, 0, 3, 4, 7, 2, 12, 1, 10, 14, 9,
     10, 6, 9, 0, 12, 11, 7, 13, 15, 1, 3, 14, 5, 2, 8, 4, 3, 15, 0, 6, 10, 1, 13, 8, 9, 4, 5, 11, 12, 7, 2, 14],
    [2, 12, 4, 1, 7, 10, 11, 6, 8, 5, 3, 15, 13, 0, 14, 9, 14, 11, 2, 12, 4, 7, 13, 1, 5, 0, 15, 10, 3, 9, 8, 6,
     4, 2, 1, 11, 10, 13, 7, 8, 15, 9, 12, 5, 6, 3, 0, 14, 11, 8, 12, 7, 1, 14, 2, 13, 6, 15, 0, 9, 10, 4, 5, 3],
    [12, 1, 10, 15, 9, 2, 6, 8, 0, 13, 3, 4, 14, 7, 5, 11, 10, 15, 4, 2, 7, 12, 9, 5, 6, 1, 13, 14, 0, 11, 3, 8,
     9, 14, 15, 5, 2, 8, 12, 3, 7, 0, 4, 10, 1, 13, 11, 6, 4, 3, 2, 12, 9, 5, 15, 10, 11, 14, 1, 7, 6, 0, 8, 13],
    [4, 11, 2, 14, 15, 0, 8, 13, 3, 12, 9, 7, 5, 10, 6, 1, 13, 0, 11, 7, 4, 9, 1, 10, 14, 3, 5, 12, 2, 15, 8, 6,
     1, 4, 11, 13, 12, 3, 7, 14, 10, 15, 6, 8, 0, 5, 9, 2, 6, 11, 13, 8, 1, 4, 10, 7, 9, 5, 0, 15, 14, 2, 3, 12],
    [13, 2, 8, 4, 6, 15, 11, 1, 10, 9, 3, 14, 5, 0, 12, 7, 1, 15, 13, 8, 10, 3, 7, 4, 12, 5, 6, 11, 0, 14, 9, 2,
     7, 11, 4, 1, 9, 12, 14, 2, 0, 6, 10, 13, 15, 3, 5, 8, 2, 1, 14, 7, 4, 10, 8, 13, 15, 12, 9, 0, 3, 5, 6, 11],
]
_SHIFTS = [1, 1, 2, 2, 2, 2, 2, 2, 1, 2, 2, 2, 2, 2, 2, 1]


def _permute(value, table, width):
    out = 0
    for position in table:
        out = (out << 1) | ((value >> (width - position)) & 1)
    return out


def _des_encrypt_block(key8, block8):
    key = _permute(int.from_bytes(key8, "big"), _PC1, 64)
    c, d = key >> 28, key & 0xFFFFFFF
    subkeys = []
    for shift in _SHIFTS:
        c = ((c << shift) | (c >> (28 - shift))) & 0xFFFFFFF
        d = ((d << shift) | (d >> (28 - shift))) & 0xFFFFFFF
        subkeys.append(_permute((c << 28) | d, _PC2, 56))
    block = _permute(int.from_bytes(block8, "big"), _IP, 64)
    left, right = block >> 32, block & 0xFFFFFFFF
    for subkey in subkeys:
        expanded = _permute(right, _E, 32) ^ subkey
        mixed = 0
        for i in range(8):
            six = (expanded >> (42 - 6 * i)) & 0x3F
            row = ((six & 0x20) >> 4) | (six & 1)
            mixed = (mixed << 4) | _S[i][row * 16 + ((six >> 1) & 0xF)]
        left, right = right, left ^ _permute(mixed, _P, 32)
    return _permute((right << 32) | left, _FP, 64).to_bytes(8, "big")


def _vnc_response(password, challenge):
    # VNC uses the first eight bytes of the password with each byte's bits reversed.
    key = bytes(int(f"{b:08b}"[::-1], 2) for b in password[:8].ljust(8, b"\0"))
    return _des_encrypt_block(key, challenge[:8]) + _des_encrypt_block(key, challenge[8:])


# --- AES-128, for Apple's user authentication only -----------------------------
def _aes_tables():
    sbox, p, q = [0] * 256, 1, 1
    while True:
        p = p ^ ((p << 1) & 0xFF) ^ (0x1B if p & 0x80 else 0)
        q ^= q << 1
        q ^= q << 2
        q ^= q << 4
        q &= 0xFF
        if q & 0x80:
            q ^= 0x09
        x = q ^ (q << 1 | q >> 7) & 0xFF ^ (q << 2 | q >> 6) & 0xFF ^ (q << 3 | q >> 5) & 0xFF ^ (q << 4 | q >> 4) & 0xFF
        sbox[p] = (x ^ 0x63) & 0xFF
        if p == 1:
            break
    sbox[0] = 0x63
    return sbox


_SBOX = _aes_tables()


def _xtime(a):
    return ((a << 1) ^ 0x1B) & 0xFF if a & 0x80 else a << 1


def _aes128_encrypt_block(key, block):
    words = [list(key[i:i + 4]) for i in range(0, 16, 4)]
    rcon = 1
    for i in range(4, 44):
        t = list(words[i - 1])
        if i % 4 == 0:
            t = [_SBOX[b] for b in t[1:] + t[:1]]
            t[0] ^= rcon
            rcon = _xtime(rcon)
        words.append([a ^ b for a, b in zip(words[i - 4], t)])
    keys = [sum(words[r * 4:r * 4 + 4], []) for r in range(11)]
    state = [b ^ k for b, k in zip(block, keys[0])]
    for rnd in range(1, 11):
        state = [_SBOX[b] for b in state]
        state = [state[(i + 4 * (i % 4)) % 16] for i in range(16)]
        if rnd < 10:
            mixed = []
            for c in range(4):
                col = state[c * 4:c * 4 + 4]
                total = col[0] ^ col[1] ^ col[2] ^ col[3]
                mixed += [col[i] ^ total ^ _xtime(col[i] ^ col[(i + 1) % 4]) for i in range(4)]
            state = mixed
        state = [b ^ k for b, k in zip(state, keys[rnd])]
    return bytes(state)


def _apple_response(user, password, generator, prime, server_public, size):
    """Apple Remote Desktop authentication (security type 30): a Diffie-Hellman
    exchange, then the user name and password, each in 64 bytes, under AES-128
    keyed by the MD5 of the shared secret."""
    import hashlib
    private = int.from_bytes(os.urandom(size), "big") % (prime - 2) + 1
    public = pow(generator, private, prime)
    shared = pow(server_public, private, prime).to_bytes(size, "big")
    key = hashlib.md5(shared).digest()

    def field(value):
        value = value[:63] + b"\0"
        return value + os.urandom(64 - len(value))

    credentials = field(user) + field(password)
    cipher = b"".join(_aes128_encrypt_block(key, credentials[i:i + 16]) for i in range(0, 128, 16))
    return cipher + public.to_bytes(size, "big")


# --- keys ---------------------------------------------------------------------
KEYSYMS = {
    "return": 0xFF0D, "enter": 0xFF0D, "escape": 0xFF1B, "esc": 0xFF1B, "tab": 0xFF09, "backspace": 0xFF08,
    "delete": 0xFFFF, "space": 0x20, "question": 0x3F, "bracketright": 0x5D, "bracketleft": 0x5B, "plus": 0x2B, "left": 0xFF51, "up": 0xFF52, "right": 0xFF53, "down": 0xFF54,
    "page_up": 0xFF55, "page_down": 0xFF56, "home": 0xFF50, "end": 0xFF57,
    "shift": 0xFFE1, "ctrl": 0xFFE3, "control": 0xFFE3, "opt": 0xFFE9, "option": 0xFFE9, "alt": 0xFFE9,
    "cmd": 0xFFEB, "command": 0xFFEB,
}
KEYSYMS.update({f"f{n}": 0xFFBD + n for n in range(1, 13)})
MODIFIERS = {"shift", "ctrl", "control", "opt", "option", "alt", "cmd", "command"}


def keysym(name):
    lower = name.lower()
    if lower in KEYSYMS:
        return KEYSYMS[lower]
    if len(name) == 1:
        code = ord(name)
        return code if code < 0x100 else 0x01000000 | code
    raise SystemExit(f"vnc: unknown key {name!r}")


class Client:
    def __init__(self, host, password, user=None):
        self.sock = socket.create_connection((host, 5900), timeout=20)
        self.down_keys = []
        self.buttons = 0
        self.position = (0, 0)
        self._handshake(password, user)

    def _read(self, n):
        chunks = []
        while n:
            chunk = self.sock.recv(min(n, 1 << 20))
            if not chunk:
                raise SystemExit("vnc: the connection closed")
            chunks.append(chunk)
            n -= len(chunk)
        return b"".join(chunks)

    def _handshake(self, password, user):
        self._read(12)
        self.sock.sendall(b"RFB 003.008\n")
        count = self._read(1)[0]
        if count == 0:
            raise SystemExit("vnc: the server refused the connection")
        offered = self._read(count)
        if user:
            # As a user of that Mac: the viewer joins the desktop that user is
            # logged in to. The shared VNC password gets a login session of
            # its own instead, which cannot join one already in use.
            if 30 not in offered:
                raise SystemExit("vnc: the server does not offer user authentication")
            self.sock.sendall(b"\x1e")
            generator, size = struct.unpack(">HH", self._read(4))
            prime = int.from_bytes(self._read(size), "big")
            server_public = int.from_bytes(self._read(size), "big")
            self.sock.sendall(_apple_response(user.encode(), password, generator, prime, server_public, size))
        else:
            if 2 not in offered:
                raise SystemExit("vnc: the server does not offer password authentication")
            self.sock.sendall(b"\x02")
            self.sock.sendall(_vnc_response(password, self._read(16)))
        if struct.unpack(">I", self._read(4))[0] != 0:
            raise SystemExit("vnc: authentication failed")
        self.sock.sendall(b"\x01")  # shared: never disconnect another viewer
        self.width, self.height = struct.unpack(">HH", self._read(4))
        self._read(16)
        self._read(struct.unpack(">I", self._read(4))[0])
        # 32 bits per pixel, little endian, red in the low byte.
        self.sock.sendall(struct.pack(">BxxxBBBBHHHBBBxxx", 0, 32, 24, 0, 1, 255, 255, 255, 0, 8, 16))
        self.sock.sendall(struct.pack(">BxHi", 2, 1, 0))  # raw encoding only

    # input
    def key(self, sym, down):
        self.sock.sendall(struct.pack(">BBxxI", 4, 1 if down else 0, sym))
        if down:
            self.down_keys.append(sym)
        elif sym in self.down_keys:
            self.down_keys.remove(sym)
        time.sleep(0.03)

    def pointer(self, x, y, buttons=None):
        if buttons is not None:
            self.buttons = buttons
        self.position = (int(x), int(y))
        self.sock.sendall(struct.pack(">BBHH", 5, self.buttons, int(x), int(y)))
        time.sleep(0.02)

    def release_everything(self):
        try:
            if self.buttons:
                self.pointer(*self.position, 0)
            for sym in list(reversed(self.down_keys)):
                self.key(sym, False)
            # Belt and braces: every modifier up, held by this client or not.
            for name in ("shift", "ctrl", "opt", "cmd"):
                self.sock.sendall(struct.pack(">BBxxI", 4, 0, KEYSYMS[name]))
            time.sleep(0.1)
        except OSError:
            pass

    def chord(self, text):
        parts = text.split("+") if len(text) > 1 else [text]
        if text.endswith("++"):
            parts = text[:-2].split("+") + ["+"]
        syms = [keysym(p) for p in parts]
        for sym in syms:
            self.key(sym, True)
        for sym in reversed(syms):
            self.key(sym, False)
        time.sleep(0.12)

    SHIFTED = set('ABCDEFGHIJKLMNOPQRSTUVWXYZ~!@#$%^&*()_+{}|:"<>?')

    def type(self, text):
        # A viewer forwards the keyboard's own Shift with a shifted character,
        # and the server types the plain key without it.
        shift = KEYSYMS["shift"]
        for ch in text:
            sym = 0xFF0D if ch == "\n" else keysym(ch)
            if ch in self.SHIFTED:
                self.key(shift, True)
            self.key(sym, True)
            self.key(sym, False)
            if ch in self.SHIFTED:
                self.key(shift, False)

    def held(self, mods):
        return [keysym(m) for m in mods.split("+")] if mods else []

    def click(self, x, y, mods="", button=1, count=1):
        self.pointer(x - 3, y)
        self.pointer(x, y)
        time.sleep(0.15)
        syms = self.held(mods)
        for sym in syms:
            self.key(sym, True)
        time.sleep(0.25 if syms else 0)
        for _ in range(count):
            self.pointer(x, y, button)
            time.sleep(0.05)
            self.pointer(x, y, 0)
            time.sleep(0.06)
        for sym in reversed(syms):
            self.key(sym, False)
        time.sleep(0.2)

    def drag(self, x1, y1, x2, y2, mods=""):
        self.pointer(x1 - 2, y1)
        self.pointer(x1, y1)
        time.sleep(0.15)
        syms = self.held(mods)
        for sym in syms:
            self.key(sym, True)
        time.sleep(0.2 if syms else 0)
        self.pointer(x1, y1, 1)
        time.sleep(0.1)
        steps = max(20, int(max(abs(x2 - x1), abs(y2 - y1)) / 3))
        for i in range(1, steps + 1):
            self.pointer(x1 + (x2 - x1) * i / steps, y1 + (y2 - y1) * i / steps)
        time.sleep(0.15)
        self.pointer(x2, y2, 0)
        for sym in reversed(syms):
            self.key(sym, False)
        time.sleep(0.2)

    def scroll(self, x, y, lines):
        self.pointer(x, y)
        time.sleep(0.1)
        button = 8 if lines > 0 else 16
        for _ in range(abs(lines)):
            self.pointer(x, y, button)
            self.pointer(x, y, 0)
            time.sleep(0.03)
        time.sleep(0.2)

    # pictures
    def capture(self, x, y, w, h):
        self.sock.sendall(struct.pack(">BBHHHH", 3, 0, x, y, w, h))
        rows = [bytearray(w * 4) for _ in range(h)]
        covered = 0
        deadline = time.time() + 30
        while covered < w * h and time.time() < deadline:
            kind = self._read(1)[0]
            if kind == 0:
                count = struct.unpack(">xH", self._read(3))[0]
                for _ in range(count):
                    rx, ry, rw, rh, encoding = struct.unpack(">HHHHi", self._read(12))
                    if encoding != 0:
                        raise SystemExit(f"vnc: unexpected encoding {encoding}")
                    data = self._read(rw * rh * 4)
                    for row in range(rh):
                        ty = ry + row - y
                        if 0 <= ty < h:
                            x0, x1 = max(rx, x), min(rx + rw, x + w)
                            if x1 > x0:
                                src = (row * rw + (x0 - rx)) * 4
                                rows[ty][(x0 - x) * 4:(x1 - x) * 4] = data[src:src + (x1 - x0) * 4]
                                covered += x1 - x0
            elif kind == 2:
                pass
            elif kind == 3:
                self._read(struct.unpack(">xxxI", self._read(7))[0])
            elif kind == 1:
                self._read(struct.unpack(">xHH", self._read(5))[1] * 6)
            else:
                raise SystemExit(f"vnc: unexpected server message {kind}")
        return rows

    def shot(self, path, region):
        x, y, w, h = region or (0, 0, self.width, self.height)
        w, h = min(w, self.width - x), min(h, self.height - y)
        rows = self.capture(x, y, w, h)
        raw = bytearray()
        for row in rows:
            raw.append(0)
            rgb = bytearray(w * 3)
            rgb[0::3], rgb[1::3], rgb[2::3] = row[0::4], row[1::4], row[2::4]
            raw += rgb

        def chunk(tag, data):
            body = tag + data
            return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body))

        with open(path, "wb") as f:
            f.write(b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0))
                    + chunk(b"IDAT", zlib.compress(bytes(raw), 6)) + chunk(b"IEND", b""))


def run(client, commands, out):
    """Carries out parsed commands on an open connection."""
    for command in commands:
        if not command:
            continue
        verb, args = command[0], command[1:]
        n = [float(a) for a in args if a.replace(".", "", 1).replace("-", "", 1).isdigit()]
        if verb == "size":
            out.append(f"{client.width} {client.height}")
        elif verb == "key":
            for chord in args:
                client.chord(chord)
        elif verb == "type":
            client.type(args[0])
        elif verb == "typefile":
            with open(os.path.expanduser(args[0]), "r", encoding="utf-8") as secret:
                client.type(secret.read().rstrip("\n"))
        elif verb == "move":
            client.pointer(n[0], n[1])
        elif verb == "click":
            client.click(n[0], n[1], args[2] if len(args) > 2 else "")
        elif verb == "dclick":
            client.click(n[0], n[1], count=2)
        elif verb == "rclick":
            client.click(n[0], n[1], button=4)
        elif verb == "drag":
            client.drag(n[0], n[1], n[2], n[3], args[4] if len(args) > 4 else "")
        elif verb == "scroll":
            client.scroll(n[0], n[1], int(n[2]))
        elif verb == "shot":
            client.shot(args[0], tuple(int(v) for v in n[-4:]) if len(n) >= 4 else None)
        elif verb == "sleep":
            time.sleep(n[0])
        else:
            raise SystemExit(f"vnc: unknown command {verb!r}")


def split(argv):
    commands, current = [], []
    for word in argv:
        if word == "--":
            commands.append(current)
            current = []
        else:
            current.append(word)
    commands.append(current)
    return commands


def connect_client():
    host = os.environ.get("AMX_VNC_HOST") or os.environ.get("AMX_MAC_TEST_HOST", "").split("@")[-1]
    path = os.environ.get("AMX_VNC_PASSWORD_FILE")
    if not host or not path:
        raise SystemExit("vnc: AMX_VNC_HOST (or AMX_MAC_TEST_HOST) and AMX_VNC_PASSWORD_FILE are required")
    if socket.gethostbyname(host) in ("127.0.0.1", socket.gethostbyname(socket.gethostname())):
        raise SystemExit("vnc: the host is this machine; nothing was sent")
    with open(os.path.expanduser(path), "rb") as f:
        return Client(host, f.read().strip(), os.environ.get("AMX_VNC_USER"))


def daemon(path):
    """Holds one connection open and serves command lists sent to a socket.

    macOS locks the screen when a viewer leaves, so a run keeps one viewer for
    its whole length rather than connecting for each action.
    """
    import json
    if os.path.exists(path):
        os.unlink(path)
    server = socket.socket(socket.AF_UNIX)
    server.bind(path)
    os.chmod(path, 0o600)
    server.listen(4)
    client = connect_client()
    try:
        while True:
            # Whatever the server says unasked (bell, clipboard) is read and dropped.
            ready, _, _ = select.select([server, client.sock], [], [], 1.0)
            if client.sock in ready:
                try:
                    kind = client.sock.recv(1)
                    if not kind:
                        raise OSError("closed")
                    if kind == b"\x03":
                        client._read(struct.unpack(">xxxI", client._read(7))[0])
                    elif kind == b"\x00":
                        for _ in range(struct.unpack(">xH", client._read(3))[0]):
                            _, _, rw, rh, _ = struct.unpack(">HHHHi", client._read(12))
                            client._read(rw * rh * 4)
                except (OSError, SystemExit):
                    client = connect_client()
            if server not in ready:
                continue
            conn, _ = server.accept()
            with conn:
                request = b""
                while not request.endswith(b"\n"):
                    chunk = conn.recv(65536)
                    if not chunk:
                        break
                    request += chunk
                argv = json.loads(request or b"[]")
                if argv == ["quit"]:
                    conn.sendall(json.dumps({"status": 0, "out": []}).encode() + b"\n")
                    return 0
                out, status = [], 0
                try:
                    run(client, split(argv), out)
                except SystemExit as error:
                    out.append(str(error))
                    status = 1
                except OSError as error:
                    out.append(f"vnc: {error}; reconnecting")
                    status = 1
                    client = connect_client()
                finally:
                    client.release_everything()
                conn.sendall(json.dumps({"status": status, "out": out}).encode() + b"\n")
    finally:
        client.release_everything()
        client.sock.close()
        if os.path.exists(path):
            os.unlink(path)


def main(argv):
    if not argv or argv[0] in ("-h", "--help"):
        print(__doc__)
        return 0
    if argv[0] == "daemon":
        return daemon(argv[1])
    held = os.environ.get("AMX_VNC_SOCKET")
    if held:
        import json
        conn = socket.socket(socket.AF_UNIX)
        conn.connect(held)
        conn.sendall(json.dumps(argv).encode() + b"\n")
        reply = b""
        while not reply.endswith(b"\n"):
            chunk = conn.recv(65536)
            if not chunk:
                break
            reply += chunk
        answer = json.loads(reply)
        for line in answer["out"]:
            print(line)
        return answer["status"]
    client = connect_client()
    out = []
    try:
        run(client, split(argv), out)
    finally:
        client.release_everything()
        client.sock.close()
        for line in out:
            print(line)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
