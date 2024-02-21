// All of the Node.js APIs are available in the preload process.
// It has the same sandbox as a Chrome extension.
const { contextBridge, ipcRenderer } = require('electron/renderer')

window.TX_API = {
  onUpdateTX: (callback) => ipcRenderer.on('updateTX', (_event, value) => callback(value)),
  onUpdateMsg: (callback) => ipcRenderer.on('updateMsg', (_event, value) => callback(value))
};

window.addEventListener('DOMContentLoaded', () => {
  const replaceText = (selector, text) => {
    const element = document.getElementById(selector)
    if (element) element.innerText = text
  }

  for (const type of ['chrome', 'node', 'electron']) {
    replaceText(`${type}-version`, process.versions[type])
  }
})

