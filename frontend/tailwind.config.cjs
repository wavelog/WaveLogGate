/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ['./index.html', './src/**/*.{svelte,js}'],
  theme: {
    extend: {
      colors: {
        surface: {
          app:     '#303030',  // body background
          header:  '#1c1c1c',  // header bar
          card:    '#262626',  // cards / panels
          section: '#2a2a2a',  // form sections
          modal:   '#383838',  // modals
          input:   '#404040',  // inputs / selects
        },
        stroke: {
          base:    '#555555',  // default border
          subtle:  '#444444',  // card borders
          section: '#404040',  // section separators
          btn:     '#666666',  // button borders
          focus:   '#888888',  // focused input
          accent:  '#5a9fd4',  // active tab / highlighted profile
        },
        fg: {
          base:      '#c6c6c6',
          bright:    '#e0e0e0',  // headings, active tab
          label:     '#aaaaaa',  // form labels
          secondary: '#999999',  // inactive tab
          muted:     '#888888',  // minor labels, timestamps
          dim:       '#666666',  // separators, disconnected
        },
        accent: {
          value:  '#55aaff',  // freq, active profile name
          orange: '#ffaa55',  // mode, bearings
          green:  '#44cc44',  // connected indicator
        },
      },
      fontSize: {
        '2xs': ['11px', { lineHeight: '1rem' }],
      },
      width: {
        'field-xs': '70px',   // narrow inputs + label column
        'field-sm': '120px',  // short text inputs
      },
    },
  },
  plugins: [],
}
