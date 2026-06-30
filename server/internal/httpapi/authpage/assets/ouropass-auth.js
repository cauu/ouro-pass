// Ouro Pass authorization / binding page driver (S0003).
//
// CIP-30 flow, browser-side and CBOR-free: discover a wallet on window.cardano,
// enable it, read the reward (stake) address, request a challenge nonce, ask the
// wallet to signData over the nonce, then forward the COSE_Key + signature to the
// backend (which recovers the stake vkey and verifies). The page holds no token:
// the authorize mode submits a hidden form so the browser natively follows the
// issuer's 302 to the client redirect_uri; the activate mode shows a deep link.
(function () {
  "use strict";

  var app = document.getElementById("op-app");
  var cfg = app ? app.dataset : {};
  var statusEl = document.getElementById("op-status");
  var listEl = document.getElementById("op-wallets");

  function setStatus(msg, isErr) {
    if (!statusEl) return;
    statusEl.textContent = msg;
    statusEl.className = isErr ? "err" : "";
  }

  function utf8ToHex(s) {
    var bytes = new TextEncoder().encode(s);
    var out = "";
    for (var i = 0; i < bytes.length; i++) {
      out += bytes[i].toString(16).padStart(2, "0");
    }
    return out;
  }

  // A CIP-30 wallet exposes an object on window.cardano with enable() plus some
  // of apiVersion/name/icon/isEnabled. Be lenient: shapes vary across wallets.
  function isWallet(w) {
    return w && typeof w.enable === "function" &&
      (w.apiVersion || w.name || w.icon || typeof w.isEnabled === "function");
  }

  function discoverWallets() {
    var c = window.cardano || {};
    var out = [];
    for (var key in c) {
      try {
        if (isWallet(c[key])) {
          out.push({ key: key, name: c[key].name || key, icon: c[key].icon || "" });
        }
      } catch (e) { /* ignore odd injected props */ }
    }
    out.sort(function (a, b) { return a.name.localeCompare(b.name); });
    return out;
  }

  function renderWallets(wallets) {
    listEl.innerHTML = "";
    wallets.forEach(function (wallet) {
      var btn = document.createElement("button");
      btn.className = "wallet";
      btn.type = "button";
      if (wallet.icon) {
        var img = document.createElement("img");
        img.src = wallet.icon;
        img.alt = "";
        img.width = 24;
        img.height = 24;
        btn.appendChild(img);
      }
      btn.appendChild(document.createTextNode(wallet.name));
      btn.addEventListener("click", function () { connect(wallet.key, btn); });
      listEl.appendChild(btn);
    });
  }

  function setBusy(busy) {
    var btns = listEl.querySelectorAll("button.wallet");
    for (var i = 0; i < btns.length; i++) btns[i].disabled = busy;
  }

  function connect(key, btn) {
    setBusy(true);
    run(key).catch(function (e) {
      setStatus((e && e.message) || "Wallet error or request rejected.", true);
    }).then(function () {
      // Authorize navigates away on success; otherwise re-enable for retry.
      setBusy(false);
    });
  }

  async function run(key) {
    setStatus("Connecting to wallet…");
    var api = await window.cardano[key].enable();

    // Network-agnostic (S0014 p1-4): the issuer has no single network; eligibility is decided
    // per-attestor against on-chain data, so the wallet's self-reported network is irrelevant.
    var rewards = await api.getRewardAddresses();
    if (!rewards || rewards.length === 0) {
      throw new Error("Wallet has no stake (reward) address; register a stake key first.");
    }
    var addr = rewards[0];

    setStatus("Requesting challenge…");
    var ch = await postJSON(cfg.challengeUrl, { purpose: cfg.purpose, stake_address: addr });
    if (!ch.ok) throw new Error(errMsg(ch.data, "challenge failed"));
    var nonce = ch.data.nonce;

    setStatus("Approve the signature request in your wallet…");
    var sig = await api.signData(addr, utf8ToHex(nonce));

    if (cfg.mode === "authorize") {
      submitAuthorize(nonce, sig);
    } else {
      await submitActivation(nonce, sig);
    }
  }

  // submitAuthorize posts a hidden form so the browser follows the 302 to the
  // client redirect_uri natively (fetch cannot read a cross-origin redirect).
  function submitAuthorize(nonce, sig) {
    setStatus("Authorizing…");
    var fields = {
      client_id: cfg.clientId,
      redirect_uri: cfg.redirectUri,
      state: cfg.state,
      aud: cfg.aud,
      scope: cfg.scope,
      nonce: nonce,
      cose_key: sig.key,
      signature: sig.signature,
      code_challenge: cfg.codeChallenge,
      device_pubkey: cfg.devicePubkey,
    };
    var form = document.createElement("form");
    form.method = "POST";
    form.action = cfg.submitUrl;
    Object.keys(fields).forEach(function (k) {
      var input = document.createElement("input");
      input.type = "hidden";
      input.name = k;
      input.value = fields[k] || "";
      form.appendChild(input);
    });
    document.body.appendChild(form);
    form.submit();
  }

  async function submitActivation(nonce, sig) {
    setStatus("Creating activation…");
    var res = await postJSON(cfg.submitUrl, {
      channel_type: cfg.channelType || "telegram",
      channel_id: cfg.channelId || "",
      nonce: nonce,
      cose_key: sig.key,
      signature: sig.signature,
    });
    if (!res.ok) throw new Error(errMsg(res.data, "activation failed"));
    showDeepLink(res.data.deep_link);
  }

  function showDeepLink(link) {
    var box = document.getElementById("op-result");
    var anchor = document.getElementById("op-deeplink");
    if (box && anchor && link) {
      anchor.href = link;
      anchor.textContent = link;
      box.style.display = "block";
    }
    setStatus("Wallet verified. Open the link to finish in Telegram.");
  }

  async function postJSON(url, body) {
    var resp = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    var data = {};
    try { data = await resp.json(); } catch (e) { /* non-JSON body */ }
    return { ok: resp.ok, data: data };
  }

  function errMsg(data, fallback) {
    return (data && (data.error_description || data.error)) || fallback;
  }

  // Wallets inject at different (sometimes late) times, so discovery is not a
  // one-shot: poll briefly and re-render as wallets appear, and also react to
  // the load event and the cardano#initialized signal some wallets dispatch.
  var lastCount = -1;
  function refresh() {
    var wallets = discoverWallets();
    if (wallets.length !== lastCount) {
      lastCount = wallets.length;
      renderWallets(wallets);
      if (wallets.length > 0) setStatus("");
    }
  }

  function init() {
    setStatus("Detecting wallet…");
    refresh();
    var tries = 0;
    var timer = setInterval(function () {
      tries++;
      refresh();
      if (lastCount > 0) {
        clearInterval(timer);
      } else if (tries >= 24) { // ~6s
        clearInterval(timer);
        setStatus("No Cardano wallet detected. Install Nami, Eternl, Lace, Typhon, Vespr, … then reload.", true);
      }
    }, 250);
  }

  window.addEventListener("load", refresh);
  window.addEventListener("cardano#initialized", refresh);
  init();
})();

