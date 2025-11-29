// Minimalista SSE client
console.log("[SSE] Script loaded");

(function () {
  let es = null;

  try {
    console.log("[SSE] Creating EventSource");
    es = new EventSource("/events");

    es.onopen = function () {
      console.log("[SSE] Connected");
    };

    es.onmessage = function (event) {
      const msg = String(event.data || "");

      // Ignore heartbeat
      if (msg.startsWith("heartbeat") || msg === "") {
        return;
      }

      console.log("[SSE] Message:", msg);

      // Handle refresh message
      if (msg === "refresh") {
        location.reload();
      }
    };

    es.onerror = function () {
      console.log("[SSE] Connection error, state:", es.readyState);
    };
  } catch (err) {
    console.log("[SSE] Error:", err);
  }

  // Cleanup on page unload
  window.addEventListener("beforeunload", function () {
    if (es) {
      es.close();
    }
  });
})();

