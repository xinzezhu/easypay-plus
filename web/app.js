const state = {
  token: localStorage.getItem("easypay_admin_token") || "",
  products: [],
  orders: [],
  view: "products",
  checkoutOrder: null,
  checkoutPoll: null,
  checkoutTimer: null,
  checkoutExpiryRequested: false,
  checkoutRedirectScheduled: false,
};
const BASE_PATH = document.querySelector('meta[name="app-base"]')?.content || "";

const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => [...document.querySelectorAll(selector)];

document.addEventListener("DOMContentLoaded", () => {
  bindCommonActions();
  refreshIcons();
  if (paymentOrderID("mock-pay")) {
    initMockCheckout();
    return;
  }
  const checkoutOrderID = paymentOrderID("pay");
  if (checkoutOrderID) {
    initCheckout(checkoutOrderID);
    return;
  }
  if (state.token) {
    openAdmin();
  } else {
    showLogin();
  }
});

function bindCommonActions() {
  $("#login-form").addEventListener("submit", login);
  $("#logout-button").addEventListener("click", logout);
  $("#refresh-button").addEventListener("click", loadAll);
  $("#add-product-button").addEventListener("click", () => $("#product-dialog").showModal());
  $("#product-form").addEventListener("submit", createProduct);
  $$(".close-dialog").forEach((button) => button.addEventListener("click", () => $("#product-dialog").close()));
  $$(".close-credentials").forEach((button) => button.addEventListener("click", () => $("#credentials-dialog").close()));
  $$(".nav-item").forEach((button) => button.addEventListener("click", () => setView(button.dataset.view)));
  $("#product-filter").addEventListener("change", loadOrders);
  $("#status-filter").addEventListener("change", loadOrders);
}

async function login(event) {
  event.preventDefault();
  const token = new FormData(event.currentTarget).get("token").trim();
  state.token = token;
  try {
    await api("/api/admin/overview");
    localStorage.setItem("easypay_admin_token", token);
    $("#login-error").hidden = true;
    await openAdmin();
  } catch (error) {
    state.token = "";
    $("#login-error").textContent = error.message;
    $("#login-error").hidden = false;
  }
}

function logout() {
  state.token = "";
  localStorage.removeItem("easypay_admin_token");
  showLogin();
}

function showLogin() {
  $("#admin-app").hidden = true;
  $("#mock-screen").hidden = true;
  $("#checkout-screen").hidden = true;
  $("#login-screen").hidden = false;
  setTimeout(() => $("#admin-token").focus(), 0);
}

async function openAdmin() {
  $("#login-screen").hidden = true;
  $("#mock-screen").hidden = true;
  $("#checkout-screen").hidden = true;
  $("#admin-app").hidden = false;
  await loadAll();
}

async function loadAll() {
  const button = $("#refresh-button");
  button.disabled = true;
  button.querySelector("svg")?.classList.add("spin");
  try {
    const [overview, products] = await Promise.all([
      api("/api/admin/overview"),
      api("/api/admin/products"),
    ]);
    state.products = products.items || [];
    renderOverview(overview);
    renderProducts();
    renderProductFilter();
    await loadOrders();
    setServiceState(true);
  } catch (error) {
    setServiceState(false);
    showToast(error.message, true);
  } finally {
    button.disabled = false;
    button.querySelector("svg")?.classList.remove("spin");
  }
}

function renderOverview(data) {
  const stats = data.stats;
  $("#metric-products").textContent = `${stats.activeProducts}/${stats.products}`;
  $("#metric-orders").textContent = stats.orders;
  $("#metric-paid").textContent = stats.paidOrders;
  $("#metric-pending").textContent = stats.pendingOrders;
  $("#metric-failed").textContent = stats.failedDelivery;
  $("#metric-amount").textContent = formatCents(stats.paidCents);
  $("#mode-badge").textContent = data.mock ? "模拟通道" : "易支付通道";
}

function renderProducts() {
  const body = $("#products-body");
  body.innerHTML = state.products.map((product) => `
    <tr>
      <td><span class="cell-main">${escapeHTML(product.name)}</span><span class="cell-sub">${escapeHTML(product.code)}</span></td>
      <td><span class="cell-sub" title="${escapeHTML(product.id)}">${escapeHTML(product.id)}</span></td>
      <td><div class="url-cell" title="${escapeHTML(product.notifyUrl)}">${escapeHTML(product.notifyUrl)}</div></td>
      <td>${statusBadge(product.status)}</td>
      <td>${formatDate(product.createdAt)}</td>
      <td class="align-right"><button class="text-button product-status" data-id="${escapeHTML(product.id)}" data-next="${product.status === "active" ? "disabled" : "active"}">${product.status === "active" ? "停用" : "启用"}</button></td>
    </tr>`).join("");
  $("#products-empty").hidden = state.products.length > 0;
  $("#product-count").textContent = `${state.products.length} 个产品`;
  $$(".product-status").forEach((button) => button.addEventListener("click", () => changeProductStatus(button)));
}

function renderProductFilter() {
  const select = $("#product-filter");
  const current = select.value;
  select.innerHTML = `<option value="">全部产品</option>${state.products.map((product) => `<option value="${escapeHTML(product.id)}">${escapeHTML(product.name)}</option>`).join("")}`;
  select.value = current;
}

async function loadOrders() {
  const query = new URLSearchParams({ limit: "200" });
  if ($("#product-filter").value) query.set("productId", $("#product-filter").value);
  if ($("#status-filter").value) query.set("status", $("#status-filter").value);
  const data = await api(`/api/admin/orders?${query}`);
  state.orders = data.items || [];
  renderOrders();
}

function renderOrders() {
  $("#orders-body").innerHTML = state.orders.map((order) => {
    const canRetry = order.deliveryId && ["failed", "retrying"].includes(order.deliveryStatus);
    return `<tr>
      <td><span class="cell-main">${escapeHTML(order.productOrderNo)}</span><span class="cell-sub" title="${escapeHTML(order.id)}">${escapeHTML(order.id)}</span></td>
      <td><span class="cell-main">${escapeHTML(order.productName || "-")}</span></td>
      <td>${order.payType === 1 ? "微信" : "支付宝"}</td>
      <td><span class="cell-main">¥${escapeHTML(order.reallyAmount || order.amount)}</span><span class="cell-sub">应付 ¥${escapeHTML(order.amount)}</span></td>
      <td>${statusBadge(order.status)}</td>
      <td>${statusBadge(order.deliveryStatus || "none", order.deliveryAttempts)}</td>
      <td>${formatDate(order.createdAt)}</td>
      <td class="align-right">${canRetry ? `<button class="text-button retry-delivery" data-id="${escapeHTML(order.deliveryId)}">重新通知</button>` : order.status === "pending" && order.payUrl ? `<a class="text-button" href="${escapeHTML(order.payUrl)}" target="_blank" rel="noopener">支付页</a>` : "-"}</td>
    </tr>`;
  }).join("");
  $("#orders-empty").hidden = state.orders.length > 0;
  $$(".retry-delivery").forEach((button) => button.addEventListener("click", () => retryDelivery(button.dataset.id)));
}

async function createProduct(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const submit = form.querySelector('[type="submit"]');
  const values = Object.fromEntries(new FormData(form));
  submit.disabled = true;
  try {
    const credentials = await api("/api/admin/products", { method: "POST", body: JSON.stringify(values) });
    form.reset();
    $("#product-dialog").close();
    showCredentials(credentials);
    await loadAll();
  } catch (error) {
    $("#product-form-error").textContent = error.message;
    $("#product-form-error").hidden = false;
  } finally {
    submit.disabled = false;
  }
}

function showCredentials(credentials) {
  const fields = [
    ["App ID", credentials.product.id],
    ["API Secret", credentials.apiSecret],
    ["Notify Secret", credentials.notifySecret],
    ["创建订单接口", `${location.origin}${BASE_PATH}/api/v1/orders`],
  ];
  $("#credential-fields").innerHTML = fields.map(([label, value]) => `
    <div class="credential-row"><label>${label}</label><div class="copy-field"><code title="${escapeHTML(value)}">${escapeHTML(value)}</code><button class="icon-button copy-value" type="button" data-value="${escapeHTML(value)}" title="复制"><i data-lucide="copy"></i></button></div></div>`).join("");
  $$(".copy-value").forEach((button) => button.addEventListener("click", async () => {
    await navigator.clipboard.writeText(button.dataset.value);
    showToast("已复制到剪贴板");
  }));
  $("#credentials-dialog").showModal();
  refreshIcons();
}

async function changeProductStatus(button) {
  button.disabled = true;
  try {
    await api(`/api/admin/products/${button.dataset.id}/status`, { method: "PATCH", body: JSON.stringify({ status: button.dataset.next }) });
    await loadAll();
    showToast(button.dataset.next === "active" ? "产品已启用" : "产品已停用");
  } catch (error) { showToast(error.message, true); }
  finally { button.disabled = false; }
}

async function retryDelivery(id) {
  try {
    await api(`/api/admin/deliveries/${id}/retry`, { method: "POST" });
    await loadOrders();
    showToast("通知已进入重试队列");
  } catch (error) { showToast(error.message, true); }
}

function setView(view) {
  state.view = view;
  $("#products-view").hidden = view !== "products";
  $("#orders-view").hidden = view !== "orders";
  $("#page-title").textContent = view === "products" ? "产品管理" : "支付订单";
  $("#add-product-button").hidden = view !== "products";
  $$(".nav-item").forEach((button) => button.classList.toggle("is-active", button.dataset.view === view));
}

async function initMockCheckout() {
  $("#admin-app").hidden = true;
  $("#login-screen").hidden = true;
  $("#checkout-screen").hidden = true;
  $("#mock-screen").hidden = false;
  const id = location.pathname.split("/").filter(Boolean).pop();
  try {
    const data = await publicAPI(`/api/mock/orders/${encodeURIComponent(id)}`);
    renderMockOrder(data.order);
    $("#mock-pay-button").addEventListener("click", async () => {
      const button = $("#mock-pay-button");
      button.disabled = true;
      try {
        const result = await publicAPI(`/api/mock/orders/${encodeURIComponent(id)}/pay`, { method: "POST" });
        renderMockOrder(result.order);
      } catch (error) { showToast(error.message, true); button.disabled = false; }
    }, { once: true });
  } catch (error) {
    $("#mock-status-label").textContent = error.message;
    $("#mock-pay-button").disabled = true;
  }
}

function renderMockOrder(order) {
  $("#mock-amount").textContent = `¥${order.reallyAmount || order.amount}`;
  $("#mock-goods").textContent = order.goodsName;
  $("#mock-product").textContent = order.productName;
  $("#mock-order-no").textContent = order.productOrderNo;
  $("#mock-pay-type").textContent = order.payType === 1 ? "微信支付" : "支付宝支付";
  const paid = order.status === "paid";
  $("#mock-status-label").textContent = paid ? "支付成功" : "等待支付";
  $(".checkout-status").classList.toggle("is-paid", paid);
  $("#mock-pay-button").disabled = paid;
  $("#mock-pay-button span").textContent = paid ? "订单已支付" : "确认模拟支付";
}

function paymentOrderID(segment) {
  const prefix = `${BASE_PATH}/${segment}/`;
  if (!location.pathname.startsWith(prefix)) return "";
  const orderID = location.pathname.slice(prefix.length).split("/")[0];
  return orderID ? decodeURIComponent(orderID) : "";
}

async function initCheckout(orderID) {
  $("#admin-app").hidden = true;
  $("#login-screen").hidden = true;
  $("#mock-screen").hidden = true;
  $("#checkout-screen").hidden = false;
  $("#checkout-return-button").addEventListener("click", () => {
    const target = $("#checkout-return-button").dataset.target;
    if (target) location.assign(target);
  });
  try {
    await loadCheckoutOrder(orderID);
    state.checkoutTimer = window.setInterval(updateCheckoutCountdown, 1000);
    state.checkoutPoll = window.setInterval(() => loadCheckoutOrder(orderID), 5000);
  } catch (error) {
    renderCheckoutError(error.message);
  }
}

async function loadCheckoutOrder(orderID) {
  const data = await publicAPI(`/api/pay/orders/${encodeURIComponent(orderID)}`);
  state.checkoutOrder = data.order;
  state.checkoutExpiryRequested = false;
  if (data.order.status !== "paid") state.checkoutRedirectScheduled = false;
  renderCheckout(data.order);
  if (!["pending", "creating"].includes(data.order.status)) stopCheckoutPolling();
}

function renderCheckout(order) {
  const localExpired = order.status === "pending" && order.expiresAt && new Date(order.expiresAt).getTime() <= Date.now();
  const displayStatus = localExpired ? "expired" : order.status;
  const statusLabel = {
    creating: "订单正在创建",
    pending: "等待支付",
    paid: "支付成功",
    expired: "订单已超时取消",
    failed: "订单创建失败",
  }[displayStatus] || "订单状态异常";
  const statusBox = $("#checkout-status");
  statusBox.classList.toggle("is-paid", displayStatus === "paid");
  statusBox.classList.toggle("is-expired", ["expired", "failed"].includes(displayStatus));
  $("#checkout-status-label").textContent = statusLabel;
  $("#checkout-amount").textContent = `¥${order.reallyAmount || order.amount}`;
  $("#checkout-goods").textContent = order.goodsName || "-";
  $("#checkout-product").textContent = order.productName || "-";
  $("#checkout-order-no").textContent = order.productOrderNo || "-";
  $("#checkout-pay-type").textContent = order.payType === 1 ? "微信支付" : "支付宝支付";

  const pending = displayStatus === "pending";
  $("#checkout-qr-section").hidden = !pending;
  if (pending) {
    const image = $("#checkout-qr-image");
    if (image.dataset.orderID !== order.id) {
      image.dataset.orderID = order.id;
      image.src = `${BASE_PATH}/pay/${encodeURIComponent(order.id)}/qrcode.png`;
    }
    $("#checkout-qr-hint").textContent = order.payType === 1 ? "请使用微信扫码完成支付" : "请使用支付宝扫码完成支付";
  } else {
    $("#checkout-qr-image").removeAttribute("src");
  }

  $("#checkout-message").textContent = displayStatus === "paid"
    ? "支付结果已确认，请返回业务页面继续。"
    : displayStatus === "expired"
      ? "二维码已失效，请返回业务页面重新创建订单。"
      : displayStatus === "failed"
        ? "该订单无法支付，请返回业务页面重新创建订单。"
        : "";
  const returnButton = $("#checkout-return-button");
  returnButton.hidden = !order.hasReturnUrl || !["paid", "expired", "failed"].includes(displayStatus);
  returnButton.dataset.target = `${BASE_PATH}/pay/${encodeURIComponent(order.id)}/return`;
  if (displayStatus === "paid" && order.hasReturnUrl) scheduleCheckoutReturn(returnButton.dataset.target);
  updateCheckoutCountdown();
}

function scheduleCheckoutReturn(target) {
  if (state.checkoutRedirectScheduled || !target) return;
  state.checkoutRedirectScheduled = true;
  $("#checkout-message").textContent = "支付结果已确认，正在返回业务页面。";
  window.setTimeout(() => location.replace(target), 1000);
}

function updateCheckoutCountdown() {
  const order = state.checkoutOrder;
  if (!order?.expiresAt) return;
  const remaining = Math.max(0, Math.ceil((new Date(order.expiresAt).getTime() - Date.now()) / 1000));
  const minutes = String(Math.floor(remaining / 60)).padStart(2, "0");
  const seconds = String(remaining % 60).padStart(2, "0");
  $("#checkout-countdown").textContent = `${minutes}:${seconds}`;
  if (remaining === 0 && order.status === "pending") {
    $("#checkout-qr-section").hidden = true;
    $("#checkout-status").classList.add("is-expired");
    $("#checkout-status-label").textContent = "订单已超时取消";
    $("#checkout-message").textContent = "二维码已失效，请返回业务页面重新创建订单。";
    if (!state.checkoutExpiryRequested) {
      state.checkoutExpiryRequested = true;
      window.setTimeout(() => loadCheckoutOrder(order.id).catch((error) => renderCheckoutError(error.message)), 0);
    }
  }
}

function renderCheckoutError(message) {
  $("#checkout-status").classList.add("is-expired");
  $("#checkout-status-label").textContent = "订单无法加载";
  $("#checkout-qr-section").hidden = true;
  $("#checkout-message").textContent = message;
  $("#checkout-countdown").textContent = "--:--";
  stopCheckoutPolling();
}

function stopCheckoutPolling() {
  if (state.checkoutPoll) window.clearInterval(state.checkoutPoll);
  if (state.checkoutTimer) window.clearInterval(state.checkoutTimer);
  state.checkoutPoll = null;
  state.checkoutTimer = null;
}

async function api(path, options = {}) {
  const headers = { ...(options.headers || {}), Authorization: `Bearer ${state.token}` };
  if (options.body) headers["Content-Type"] = "application/json";
  const response = await fetch(`${BASE_PATH}${path}`, { ...options, headers });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    if (response.status === 401 && path !== "/api/admin/overview") logout();
    throw new Error(data.error?.message || `请求失败 (${response.status})`);
  }
  return data;
}

async function publicAPI(path, options = {}) {
  const response = await fetch(`${BASE_PATH}${path}`, options);
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error?.message || `请求失败 (${response.status})`);
  return data;
}

function statusBadge(status, attempts = 0) {
  const labels = {
    active: "运行中", disabled: "已停用", creating: "创建中", pending: "待支付", paid: "已支付",
    failed: "失败", expired: "已过期", delivered: "已送达", processing: "通知中", retrying: "待重试", none: "未触发",
  };
  const suffix = attempts > 0 && ["failed", "retrying", "delivered"].includes(status) ? ` · ${attempts}次` : "";
  return `<span class="status-badge status-${escapeHTML(status)}">${labels[status] || escapeHTML(status)}${suffix}</span>`;
}

function setServiceState(online) {
  $("#service-dot").className = `state-dot ${online ? "is-online" : "is-error"}`;
  $("#service-label").textContent = online ? "服务运行正常" : "服务连接异常";
}

function formatCents(cents) { return `¥${(Number(cents || 0) / 100).toFixed(2)}`; }
function formatDate(value) { return value ? new Intl.DateTimeFormat("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }).format(new Date(value)) : "-"; }
function escapeHTML(value) { return String(value ?? "").replace(/[&<>'"]/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;" })[char]); }
function refreshIcons() { if (window.lucide) window.lucide.createIcons({ attrs: { "aria-hidden": "true" } }); }

let toastTimer;
function showToast(message, error = false) {
  const toast = $("#toast");
  toast.textContent = message;
  toast.classList.toggle("is-error", error);
  toast.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { toast.hidden = true; }, 3200);
}
