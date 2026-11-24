document.addEventListener("click", (event) => {
  if (event.target.closest(".cancel-edit")) {
    event.target.closest("details").open = false;
  }
});

document.addEventListener("htmx:beforeSwap", (event) => {
  if (event.detail.xhr.status === 422) {
    event.detail.shouldSwap = true;
    event.detail.isError = false;
  }
});

function showRequestError(message) {
  const alert = document.getElementById("network-error");
  alert.textContent = message;
  alert.hidden = false;
}

document.addEventListener("htmx:sendError", () => {
  showRequestError("Could not reach the server. Check your connection and try again.");
});

document.addEventListener("htmx:responseError", (event) => {
  showRequestError(event.detail.xhr.status === 400
    ? "Your session expired. Reload the page and try again."
    : "That change could not be saved. Reload the page and try again.");
});

document.addEventListener("htmx:afterSwap", (event) => {
  document.getElementById("network-error").hidden = true;
  if (event.detail.requestConfig && event.detail.requestConfig.path === "/todos") {
    const input = document.getElementById("new-title");
    if (input) input.focus();
  }
});
