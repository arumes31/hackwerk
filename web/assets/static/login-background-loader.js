const desktopScene = window.matchMedia("(min-width: 769px)");
const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
const loader = document.querySelector("[data-login-background-loader]");
const backgroundURL = loader?.dataset.loginBackgroundSrc || "./login-background.js";
let loaded = false;

const loadOriginalBackground = () => {
  if (loaded || !desktopScene.matches || reducedMotion.matches) return;
  loaded = true;
  import(backgroundURL);
};

loadOriginalBackground();
desktopScene.addEventListener("change", loadOriginalBackground);
reducedMotion.addEventListener("change", loadOriginalBackground);
