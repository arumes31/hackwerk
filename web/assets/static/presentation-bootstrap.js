(() => {
  const root = document.documentElement;
  try {
    root.classList.toggle("density-comfortable", localStorage.getItem("hackwerk:density") === "comfortable");
    root.classList.toggle("outdoor-contrast", localStorage.getItem("hackwerk:outdoor") === "true");
  } catch {
    // Presentation preferences are optional when browser storage is unavailable.
  }
  if (matchMedia("(display-mode: standalone)").matches || navigator.standalone === true) {
    root.classList.add("standalone");
  }
})();
