/* md-mini.js
 * Minimal Markdown toolbar for <textarea>, GitHub-like.
 * No dependencies. Works with Bootstrap classes if present.
 * Usage: <textarea data-provide="md-mini" data-preview-url="/api/markdown/preview"></textarea>
 * Security note: If preview is enabled, the server must sanitize HTML.
 */

(function () {
  "use strict";

  // Utilities
  function $(sel, root) {
    return (root || document).querySelector(sel);
  }
  function el(tag, cls, text) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }

  function getSelRange(t) {
    return { start: t.selectionStart, end: t.selectionEnd, value: t.value };
  }

  function setSelRange(t, start, end) {
    t.focus();
    t.setSelectionRange(start, end);
  }

  function wrapSelection(t, before, after, placeholder) {
    const { start, end, value } = getSelRange(t);
    const selected = value.slice(start, end) || placeholder || "";
    const newText =
      value.slice(0, start) + before + selected + after + value.slice(end);
    t.value = newText;
    const cursorStart = start + before.length;
    const cursorEnd = cursorStart + selected.length;
    setSelRange(t, cursorStart, cursorEnd);
    t.dispatchEvent(new Event("input", { bubbles: true }));
  }

  function toggleLinePrefix(t, prefix) {
    const { start, end, value } = getSelRange(t);
    const before = value.slice(0, start);
    const sel = value.slice(start, end);
    const after = value.slice(end);

    const lines = sel.split(/\n/);
    const allHave = lines.every((line) => line.startsWith(prefix));
    const newLines = lines.map((line) => {
      if (!line.trim()) return line; // keep empty lines intact
      return allHave
        ? line.replace(
            new RegExp("^" + prefix.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")),
            "",
          )
        : prefix + line;
    });

    const out = before + newLines.join("\n") + after;
    const delta = newLines.join("\n").length - sel.length;
    t.value = out;
    setSelRange(t, start, end + delta);
    t.dispatchEvent(new Event("input", { bubbles: true }));
  }

  function toggleATXHeading(t) {
    const { start, end, value } = getSelRange(t);
    const before = value.slice(0, start);
    const sel = value.slice(start, end) || "Heading";
    const after = value.slice(end);

    const lines = sel.split(/\n/).map((line) => {
      const trimmed = line.replace(/^\s+|\s+$/g, "");
      if (!trimmed) return line;
      if (/^#{1,6}\s/.test(trimmed)) {
        return trimmed.replace(/^#{1,6}\s/, ""); // remove heading
      }
      return "# " + trimmed; // add H1
    });

    const out = before + lines.join("\n") + after;
    t.value = out;
    const newEnd = start + lines.join("\n").length;
    setSelRange(t, start, newEnd);
    t.dispatchEvent(new Event("input", { bubbles: true }));
  }

  function insertLink(t) {
    const url = prompt("Link URL:", "https://");
    if (!url) return;
    wrapSelection(t, "[", "](" + url + ")", "link text");
  }

  function insertImage(t) {
    // Dispatch custom event to open image modal
    console.log("[md-mini] insertImage called");
    const evt = new CustomEvent("md-mini:insert-image", {
      detail: { textarea: t },
      bubbles: true,
    });
    console.log("[md-mini] Dispatching md-mini:insert-image event");
    t.dispatchEvent(evt);
  }

  function insertVideo(t) {
    // Dispatch custom event to open video modal
    console.log("[md-mini] insertVideo called");
    const evt = new CustomEvent("md-mini:insert-video", {
      detail: { textarea: t },
      bubbles: true,
    });
    console.log("[md-mini] Dispatching md-mini:insert-video event");
    t.dispatchEvent(evt);
  }

  function insertAudio(t) {
    // Dispatch custom event to open audio modal
    console.log("[md-mini] insertAudio called");
    const evt = new CustomEvent("md-mini:insert-audio", {
      detail: { textarea: t },
      bubbles: true,
    });
    console.log("[md-mini] Dispatching md-mini:insert-audio event");
    t.dispatchEvent(evt);
  }

  function insertCode(t) {
    const { start, end, value } = getSelRange(t);
    const sel = value.slice(start, end);
    if (sel.includes("\n")) {
      // code block
      wrapSelection(t, "```\n", "\n```", "code");
    } else {
      // inline code
      wrapSelection(t, "`", "`", sel || "code");
    }
  }

  function insertTable(t) {
    const snippet = [
      "| Column 1 | Column 2 |",
      "|----------|----------|",
      "| Value 1  | Value 2  |",
    ].join("\n");
    const { start, value } = getSelRange(t);
    const prefix =
      start > 0 && !/\n$/.test(value.slice(0, start)) ? "\n\n" : "";
    wrapSelection(t, prefix, "", snippet);
  }

  async function doPreview(textarea, previewBox, url) {
    // Strategy:
    // 1) If URL provided via data-preview-url, POST markdown and expect sanitized HTML back.
    // 2) Else, emit a custom event so the host app can render and set previewBox.innerHTML.
    previewBox.innerHTML =
      "<div class='small text-secondary'>Rendering preview…</div>";

    if (url) {
      try {
        const resp = await fetch(url, {
          method: "POST",
          headers: { "Content-Type": "text/markdown" },
          body: textarea.value,
        });
        const html = await resp.text();
        previewBox.innerHTML = html;
      } catch (e) {
        previewBox.innerHTML =
          "<div class='text-danger small'>Preview error.</div>";
      }
    } else {
      const evt = new CustomEvent("md-mini:preview", {
        detail: { markdown: textarea.value, target: previewBox },
      });
      textarea.dispatchEvent(evt);
      // Host app should handle and set previewBox.innerHTML
      if (!previewBox.innerHTML) {
        previewBox.innerHTML =
          "<div class='small text-secondary'>Provide data-preview-url or listen to 'md-mini:preview' to supply HTML.</div>";
      }
    }
  }

  function buildToolbar(textarea) {
    // Toolbar container
    const group = el("div", "md-mini-toolbar d-flex align-items-center mb-2");

    // Group for formatting buttons (poderemos ocultar tudo em preview)
    const tools = el("div", "md-mini-tools d-flex gap-1 me-2");
    tools.style.flexWrap = "wrap";
    tools.style.minWidth = "0";

    const clsBtn = "btn btn-sm btn-outline-secondary";

    const buttons = [
      {
        svg: '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="currentColor" class="bi bi-type-bold" viewBox="0 0 16 16"><path d="M8.21 13c2.106 0 3.412-1.087 3.412-2.823 0-1.306-.984-2.283-2.324-2.386v-.055a2.176 2.176 0 0 0 1.852-2.14c0-1.51-1.162-2.46-3.014-2.46H3.843V13zM5.908 4.674h1.696c.963 0 1.517.451 1.517 1.244 0 .834-.629 1.32-1.73 1.32H5.908V4.673zm0 6.788V8.598h1.73c1.217 0 1.88.492 1.88 1.415 0 .943-.643 1.449-1.832 1.449H5.907z"/></svg>',
        title: "Bold",
        on: () => wrapSelection(textarea, "**", "**", "bold"),
      },
      {
        svg: '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="currentColor" viewBox="0 0 16 16"><path d="M7.991 11.674 9.53 2h1.635L9.625 11.674H8m-1-.25h-.5c.259 0 .442.271.442.707 0 .314-.183.75-.442.75h.5z"/></svg>',
        title: "Italic",
        on: () => wrapSelection(textarea, "*", "*", "italic"),
      },
      {
        svg: '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="currentColor" viewBox="0 0 16 16"><path d="M8.637 13V3.669H7.379V7.62H2.758V3.67H1.5V13h1.258V8.728h4.62V13h1.259Z"/></svg>',
        title: "Heading",
        on: () => toggleATXHeading(textarea),
      },
      {
        svg: '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="currentColor" viewBox="0 0 16 16"><path d="M3 5.5a.5.5 0 0 1 .5-.5h11a.5.5 0 0 1 0 1h-11a.5.5 0 0 1-.5-.5zm0 3a.5.5 0 0 1 .5-.5h11a.5.5 0 0 1 0 1h-11a.5.5 0 0 1-.5-.5zm0 3a.5.5 0 0 1 .5-.5h11a.5.5 0 0 1 0 1h-11a.5.5 0 0 1-.5-.5z"/></svg>',
        title: "Quote",
        on: () => toggleLinePrefix(textarea, "> "),
      },
      {
        svg: '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="currentColor" viewBox="0 0 16 16"><path d="m11.596 8.697-6.363 3.692c-.54.313-1.233-.066-1.233-.697V4.308c0-.63.692-1.01 1.233-.696l6.363 3.692a.802.802 0 0 1 0 1.393z"/></svg>',
        title: "Code",
        on: () => insertCode(textarea),
      },
      {
        svg: '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="currentColor" viewBox="0 0 16 16"><path d="M5 11.5a.5.5 0 0 1 .5-.5h9a.5.5 0 0 1 0 1h-9a.5.5 0 0 1-.5-.5zm0-4a.5.5 0 0 1 .5-.5h9a.5.5 0 0 1 0 1h-9a.5.5 0 0 1-.5-.5zm0-4a.5.5 0 0 1 .5-.5h9a.5.5 0 0 1 0 1h-9a.5.5 0 0 1-.5-.5zm-3 1a1 1 0 1 1 0-2 1 1 0 0 1 0 2zm0 4a1 1 0 1 1 0-2 1 1 0 0 1 0 2zm0 4a1 1 0 1 1 0-2 1 1 0 0 1 0 2z"/></svg>',
        title: "List",
        on: () => toggleLinePrefix(textarea, "- "),
      },
      {
        svg: '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="currentColor" viewBox="0 0 16 16"><path d="M5 11.5a.5.5 0 0 1 .5-.5h9a.5.5 0 0 1 0 1h-9a.5.5 0 0 1-.5-.5zm0-4a.5.5 0 0 1 .5-.5h9a.5.5 0 0 1 0 1h-9a.5.5 0 0 1-.5-.5zm0-4a.5.5 0 0 1 .5-.5h9a.5.5 0 0 1 0 1h-9a.5.5 0 0 1-.5-.5zM1.381 14.636a.5.5 0 0 1 .375-.965h.001l.192-.03a.5.5 0 0 1 .493.582c-.043.34-.302.554-.672.554-.622 0-1.075-.211-1.331-.612a.908.908 0 0 1-.078-.254.5.5 0 1 1 .999-.057c.01.067.045.146.087.207.123.163.323.328.612.328.23 0 .4-.105.457-.205.031-.069.044-.147.01-.248-.034-.101-.069-.15-.148-.195l-.11-.066c-.153-.082-.346-.189-.5-.443-.154-.254-.279-.601-.279-1.049.005-.51.326-.763.848-.763.578 0 1.072.213 1.37.673a.5.5 0 0 1-.878.531c-.12-.198-.346-.423-.617-.487z"/></svg>',
        title: "Ordered",
        on: () => toggleLinePrefix(textarea, "1. "),
      },
      {
        svg: '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="currentColor" viewBox="0 0 16 16"><path d="m15.854 5.146a.5.5 0 0 1 0 .708l-3 3a.5.5 0 0 1-.708 0l-1.5-1.5a.5.5 0 0 1 .708-.708L12.5 7.793l2.646-2.647a.5.5 0 0 1 .708 0z"/><path d="M1 14s-1 0-1-1 1-4 6-4 6 3 6 4-1 1-1 1H1zm5-6a3 3 0 1 0 0-6 3 3 0 0 0 0 6z"/></svg>',
        title: "Link",
        on: () => insertLink(textarea),
      },
      {
        svg: '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="currentColor" class="bi bi-card-image" viewBox="0 0 16 16"><path d="M6.002 5.5a1.5 1.5 0 1 1-3 0 1.5 1.5 0 0 1 3 0"/><path d="M1.5 2A1.5 1.5 0 0 0 0 3.5v9A1.5 1.5 0 0 0 1.5 14h13a1.5 1.5 0 0 0 1.5-1.5v-9A1.5 1.5 0 0 0 14.5 2zm13 1a.5.5 0 0 1 .5.5v6l-3.775-1.947a.5.5 0 0 0-.577.093l-3.71 3.71-2.66-1.772a.5.5 0 0 0-.63.062L1.002 12v.54L1 12.5v-9a.5.5 0 0 1 .5-.5z"/></svg>',
        title: "Image",
        on: () => insertImage(textarea),
      },
      {
        svg: '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="currentColor" class="bi bi-collection-play-fill" viewBox="0 0 16 16"><path d="M2.5 3.5a.5.5 0 0 1 0-1h11a.5.5 0 0 1 0 1zm2-2a.5.5 0 0 1 0-1h7a.5.5 0 0 1 0 1zM0 13a1.5 1.5 0 0 0 1.5 1.5h13A1.5 1.5 0 0 0 16 13V6a1.5 1.5 0 0 0-1.5-1.5h-13A1.5 1.5 0 0 0 0 6zm6.258-6.437a.5.5 0 0 1 .507.013l4 2.5a.5.5 0 0 1 0 .848l-4 2.5A.5.5 0 0 1 6 12V7a.5.5 0 0 1 .258-.437"/></svg>',
        title: "Video",
        on: () => insertVideo(textarea),
      },
      {
        svg: '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="currentColor" class="bi bi-soundwave" viewBox="0 0 16 16"><path fill-rule="evenodd" d="M8.5 2a.5.5 0 0 1 .5.5v11a.5.5 0 0 1-1 0v-11a.5.5 0 0 1 .5-.5m-2 2a.5.5 0 0 1 .5.5v7a.5.5 0 0 1-1 0v-7a.5.5 0 0 1 .5-.5m4 0a.5.5 0 0 1 .5.5v7a.5.5 0 0 1-1 0v-7a.5.5 0 0 1 .5-.5m-6 1.5A.5.5 0 0 1 5 6v4a.5.5 0 0 1-1 0V6a.5.5 0 0 1 .5-.5m8 0a.5.5 0 0 1 .5.5v4a.5.5 0 0 1-1 0V6a.5.5 0 0 1 .5-.5m-10 1A.5.5 0 0 1 3 7v2a.5.5 0 0 1-1 0V7a.5.5 0 0 1 .5-.5m12 0a.5.5 0 0 1 .5.5v2a.5.5 0 0 1-1 0V7a.5.5 0 0 1 .5-.5"/></svg>',
        title: "Audio",
        on: () => insertAudio(textarea),
      },
      {
        svg: '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="currentColor" viewBox="0 0 16 16"><path d="M0 2a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H2a2 2 0 0 1-2-2V2zm15 2h-4v3h4V4zm0 4h-4v3h4V8zm0 4h-4v3h4v-3zM4 4h3v3H4V4zm0 4h3v3H4V8zm0 4h3v3H4v-3z"/></svg>',
        title: "Table",
        on: () => insertTable(textarea),
      },
    ];

    buttons.forEach((b) => {
      const btn = el("button", clsBtn);
      btn.type = "button";
      btn.title = b.title;
      btn.innerHTML = b.svg;
      btn.style.minWidth = "32px";
      btn.style.minHeight = "32px";
      btn.style.display = "flex";
      btn.style.alignItems = "center";
      btn.style.justifyContent = "center";
      btn.setAttribute("data-button-title", b.title);
      btn.setAttribute("data-svg", b.svg); // Store SVG for menu cloning
      btn.addEventListener("click", b.on);
      tools.appendChild(btn);
    });

    // Hamburger menu for buttons that do not fit
    const moreBtn = el("button", clsBtn);
    moreBtn.type = "button";
    moreBtn.title = "More";
    moreBtn.innerHTML =
      '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="currentColor" viewBox="0 0 16 16"><path d="M3 9.5a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3z"/></svg>';
    moreBtn.style.minWidth = "32px";
    moreBtn.style.minHeight = "32px";
    moreBtn.style.display = "none";
    moreBtn.style.alignItems = "center";
    moreBtn.style.justifyContent = "center";
    moreBtn.style.position = "relative";
    moreBtn.setAttribute("data-more", "true"); // Mark hamburger to exclude from overflow calc

    // Dropdown menu for extra buttons
    const moreMenu = el("div");
    moreMenu.style.position = "absolute";
    moreMenu.style.bottom = "100%";
    moreMenu.style.right = "0";
    moreMenu.style.marginBottom = "0.5rem";
    moreMenu.style.backgroundColor = "var(--bs-body-bg, white)";
    moreMenu.style.color = "var(--bs-body-color, black)";
    moreMenu.style.border = "1px solid var(--bs-border-color, #ccc)";
    moreMenu.style.borderRadius = "0.375rem";
    moreMenu.style.boxShadow = "0 2px 8px rgba(0,0,0,0.15)";
    moreMenu.style.zIndex = "1000";
    moreMenu.style.minWidth = "150px";
    moreMenu.style.display = "none";
    moreMenu.style.flexDirection = "column";
    moreMenu.style.gap = "0.25rem";
    moreMenu.style.padding = "0.25rem";

    // Close menu when clicking outside
    document.addEventListener("click", (e) => {
      if (e.target !== moreBtn && !moreMenu.contains(e.target)) {
        moreMenu.style.display = "none";
      }
    });

    moreBtn.addEventListener("click", (e) => {
      e.stopPropagation();
      moreMenu.style.display =
        moreMenu.style.display === "none" ? "flex" : "none";
    });

    moreBtn.appendChild(moreMenu);
    tools.appendChild(moreBtn);

    group.appendChild(tools);

    // Function to reposition buttons based on available space
    const updateToolbarResponsiveness = () => {
      const toolsRect = tools.getBoundingClientRect();
      const availableWidth = window.innerWidth - 200; // safety margin

      const visibleBtns = Array.from(
        tools.querySelectorAll("button:not([data-more])"),
      );

      // Clear menu
      moreMenu.innerHTML = "";
      visibleBtns.forEach((btn) => (btn.style.display = "flex"));
      moreBtn.style.display = "none";

      // If buttons do not fit, move them to the menu
      if (toolsRect.width > availableWidth && visibleBtns.length > 4) {
        const mainCount = Math.max(3, Math.floor((availableWidth - 50) / 40));
        for (let i = mainCount; i < visibleBtns.length; i++) {
          const origBtn = visibleBtns[i];
          origBtn.style.display = "none";

          // Create menu button with SVG and text
          const menuBtn = el("button", clsBtn);
          menuBtn.type = "button";
          menuBtn.title = origBtn.title;
          menuBtn.style.width = "100%";
          menuBtn.style.justifyContent = "flex-start";
          menuBtn.style.paddingLeft = "0.5rem";
          menuBtn.style.paddingRight = "0.5rem";
          menuBtn.style.gap = "0.5rem";

          // Container for SVG and text
          const svgContainer = el("div");
          svgContainer.style.display = "flex";
          svgContainer.style.alignItems = "center";
          svgContainer.style.width = "16px";
          svgContainer.style.height = "16px";
          svgContainer.style.flexShrink = "0";
          const svgData = origBtn.getAttribute("data-svg");
          svgContainer.innerHTML = svgData;

          // Button text
          const textSpan = el("span");
          textSpan.textContent = origBtn.getAttribute("data-button-title");
          textSpan.style.flex = "1";

          menuBtn.appendChild(svgContainer);
          menuBtn.appendChild(textSpan);

          menuBtn.addEventListener("click", () => {
            // Execute the original button action
            const clickEvent = new MouseEvent("click", {
              bubbles: true,
              cancelable: true,
            });
            origBtn.dispatchEvent(clickEvent);
            moreMenu.style.display = "none";
          });
          moreMenu.appendChild(menuBtn);
        }

        if (moreMenu.children.length > 0) {
          moreBtn.style.display = "flex";
        }
      }
    };

    // Reposition on load and on window resize
    setTimeout(updateToolbarResponsiveness, 0);
    window.addEventListener("resize", updateToolbarResponsiveness);

    // Preview toggle (visible in preview mode too, like GitHub)
    const previewBtn = el("button", clsBtn, "Preview");
    previewBtn.type = "button";
    previewBtn.setAttribute("data-state", "off");

    const spacer = el("div", "flex-grow-1");
    group.appendChild(spacer);
    group.appendChild(previewBtn);

    // Preview box occupies the visual space of textarea when active
    const previewBox = el("div", "md-mini-preview border rounded p-2 d-none");
    previewBox.style.minHeight = "2.5rem";

    // Edit/Preview toggle logic GitHub-style
    previewBtn.addEventListener("click", () => {
      const state = previewBtn.getAttribute("data-state");
      const url = textarea.getAttribute("data-preview-url");

      if (state === "off") {
        // Entering Preview: hide textarea and buttons, show HTML
        previewBtn.setAttribute("data-state", "on");
        previewBtn.classList.add("active");
        previewBtn.textContent = "Edit";

        textarea.classList.add("d-none");
        tools.classList.add("d-none");
        previewBox.classList.remove("d-none");

        doPreview(textarea, previewBox, url);
      } else {
        // Returning to edit mode
        previewBtn.setAttribute("data-state", "off");
        previewBtn.classList.remove("active");
        previewBtn.textContent = "Preview";

        textarea.classList.remove("d-none");
        tools.classList.remove("d-none");
        previewBox.classList.add("d-none");
      }
    });

    return { toolbar: group, previewBox, tools };
  }

  function enhance(textarea) {
    if (textarea.dataset.mdMiniReady === "1") return;
    textarea.dataset.mdMiniReady = "1";

    const parent = textarea.parentNode;

    const { toolbar, previewBox } = buildToolbar(textarea);

    // Toolbar antes do textarea
    parent.insertBefore(toolbar, textarea);
    // PreviewBox right after textarea (visually replaces textarea when active)
    parent.insertBefore(previewBox, textarea.nextSibling);

    // Keyboard shortcuts (GitHub-like basics)
    textarea.addEventListener("keydown", (e) => {
      if (!e.ctrlKey && !e.metaKey) return;
      const k = e.key.toLowerCase();
      if (k === "b") {
        e.preventDefault();
        wrapSelection(textarea, "**", "**", "bold");
      } else if (k === "i") {
        e.preventDefault();
        wrapSelection(textarea, "*", "*", "italic");
      } else if (k === "k") {
        e.preventDefault();
        insertLink(textarea);
      }
    });
  }

  function init() {
    const nodes = document.querySelectorAll('textarea[data-provide="md-mini"]');
    nodes.forEach(enhance);
  }

  if (
    document.readyState === "complete" ||
    document.readyState === "interactive"
  ) {
    init();
  } else {
    document.addEventListener("DOMContentLoaded", init);
  }
})();

