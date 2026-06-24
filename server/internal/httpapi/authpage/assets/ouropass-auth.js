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

  // Wallets expose themselves as objects on window.cardano with an enable()
  // method and an apiVersion. Keys like "enable"/"isEnabled" are not wallets.
  function discoverWallets() {
    var c = window.cardano || {};
    var out = [];
    for (var key in c) {
      var w = c[key];
      if (w && typeof w.enable === "function" && (w.apiVersion || w.name)) {
        out.push({ key: key, name: w.name || key, icon: w.icon || "" });
      }
    }
    out.sort(function (a, b) { return a.name.localeCompare(b.name); });
    return out;
  }

  function renderWallets() {
    var wallets = discoverWallets();
    listEl.innerHTML = "";
    if (wallets.length === 0) {
      setStatus("No Cardano wallet detected. Install Nami, Eternl, Lace, Typhon, …", true);
      return;
    }
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

    // Network guard: only enforced when the issuer declares its network.
    if (cfg.network) {
      var netId = await api.getNetworkId();
      var want = cfg.network === "mainnet" ? 1 : 0;
      if (netId !== want) {
        throw new Error("Wallet is on the wrong network for this issuer.");
      }
    }

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

  renderWallets();
})();
