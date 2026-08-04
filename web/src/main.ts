import { mount } from "svelte";
import App from "./App.svelte";
import { startPresence } from "./presence";
import "./app.css";
import "@xterm/xterm/css/xterm.css";

startPresence();

const app = mount(App, { target: document.getElementById("app")! });

export default app;
