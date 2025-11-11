# WaveLogGate - CAT and WSJT-X Bridge for WaveLog

A modern Electron-based gateway application that connects WSJT-X, FLRig, Hamlib, and other amateur radio software to WaveLog for seamless logging and radio control.

# TL;DR:
- For CAT you'll need [FLRig](https://www.w1hkj.org/files/flrig/) or [Hamlib](https://github.com/Hamlib/Hamlib/wiki/Download) installed and connected to your Transceiver.
- For logging QSOs from WSJT-X, you need to configure the so called "Secondary UDP Server" like shown in the picture:
<img width="788" height="312" alt="image" src="https://github.com/user-attachments/assets/a4d005d0-8546-4ae3-99e8-89a195df9e0e" />


## Features

### Core Functionality
- **Automatic QSO Logging**: Real-time logging from WSJT-X, FLDigi, and any software sending ADIF via UDP
- **CAT Radio Control**: Full radio control via FLRig or Hamlib integration
- **Dual Profile Support**: Switch between two complete configuration profiles
- **Real-time Radio Status**: Live frequency, mode, and power status updates to WaveLog
- **Cross-platform**: Windows, macOS, and Linux support

### Advanced Features
- **WebSocket Server**: Real-time broadcasting of radio status changes to external clients
- **HTTP API**: Simple frequency/mode control endpoint for external integrations
- **Power Monitoring**: Automatic power level reporting (can be disabled if needed)
- **Split Operation**: Support for split frequency operations
- **ADIF Processing**: Robust ADIF and XML parsing with automatic band detection
- **Modern UI**: Bootstrap 4-based interface with responsive design

## Prerequisites

- **WaveLog Instance**: Any WaveLog installation with HTTPS (SSL) enabled
- **WaveLog API Key**: Generated from WaveLog right menu → API-Keys
- **WaveLog Station ID**: Found in WaveLog right menu → Station locations
- **Radio Control Software** (optional):
  - FLRig for CAT control
  - Hamlib for CAT control
  - OR any software capable of sending ADIF via UDP
- **WSJT-X** (optional): For automatic digital mode logging

## Installation

### Download Pre-built Binaries
1. Download the latest release from the [WaveLogGate GitHub repository](https://github.com/wavelog/WaveLogGate/releases)
2. Run the installer for your platform:
   - **Windows**: Run the `.exe` installer
   - **macOS**: Copy the `.app` file to Applications folder
   - **Linux**: Install the `.deb` package or extract the AppImage

### Apple Silicon Mac Users
Due to macOS security restrictions for unsigned apps:

```bash
# After copying to Applications folder
xattr -d com.apple.quarantine /Applications/WaveLogGate.app
```

## Configuration

### Basic Setup
1. **Launch WaveLogGate**
2. **Enter WaveLog Details**:
   - **WaveLog URL**: Full URL including `/index.php` (e.g., `https://your-wavelog.com/index.php`)
   - **API Key**: From WaveLog right menu → API-Keys
   - **Station ID**: From WaveLog right menu → Station locations (click the small badge)
3. **Configure Radio Control** (optional):
   - Select radio type: FLRig, Hamlib, or None
   - Enter host and port (default: 127.0.0.1 and appropriate port)
   - Enable/disable mode control and power monitoring
4. **Test Configuration**: Click the "Test" button - it turns green if successful
5. **Save Settings**: Click "Save" to persist your configuration

### Radio Configuration Options

#### FLRig Setup
- **Host**: Usually `127.0.0.1` if running locally
- **Port**: Default `12345`
- **Mode Control**: Enable to let WaveLogGate set radio modes automatically

#### Hamlib Setup
- **Host**: Usually `127.0.0.1` if running locally
- **Port**: Default `4532`
- **Mode Control**: Enable to let WaveLogGate set radio modes automatically
- **Ignore Power**: Check if your radio doesn't report power correctly

### Custom HTTP Headers (Optional)

If your WaveLog instance is behind Cloudflare Access or similar authentication proxy, you can configure custom HTTP headers:
- **CF-Access-Client-Id**: Client ID for Cloudflare Access authentication
- **CF-Access-Client-Secret**: Client Secret for Cloudflare Access authentication

These headers are automatically included in all requests to your WaveLog server when configured. Leave them empty if you don't need them.

### Profile Management
WaveLogGate supports two complete configuration profiles:
- Click the profile toggle button (1/2) to switch between profiles
- Each profile maintains independent WaveLog and radio settings
- Useful for multiple stations or operating locations

## Software Integration

### WSJT-X Setup
1. Open WSJT-X Settings → Reporting
2. **Enable "Secondary UDP Server"**
3. Set UDP port to **2333**
4. **Important**: Do NOT set the main "UDP Server" to port 2333

### FLDigi Setup
Configure FLDigi to send ADIF logs via UDP to port 2333.

### WaveLog Integration
1. **For Live QSOs**: Open WaveLog Live Logging → Radio tab → Select "WLGate"
2. **For Manual QSOs**: In Stations tab, select "WLGate" as radio
3. **Bandmap Control**: Click spots in WaveLog bandmap to automatically QSY your radio

## API and Integration

### HTTP API
**Endpoint**: `http://localhost:54321/{frequency}/{mode}`

Example:
```bash
# Set radio to 7.155 MHz LSB
curl http://localhost:54321/7155000/LSB
```

### WebSocket Server
**Port**: `54322`
**Protocol**: WebSocket

Real-time radio status updates:
```javascript
const ws = new WebSocket('ws://localhost:54322');
ws.onmessage = (event) => {
    const data = JSON.parse(event.data);
    if (data.type === 'radio_status') {
        console.log(`Frequency: ${data.frequency}, Mode: ${data.mode}`);
    }
};
```


## Advanced Settings

Access advanced settings by pressing **Ctrl+Shift+D** in the configuration window:

- **Force Hamlib**: Override FLRig and use Hamlib instead
- **Disable Power Transfer**: Stop sending power readings to WaveLog
- **Debug Options**: Additional logging and troubleshooting options

**Note**: Advanced settings are in beta - restart the application after changes to ensure they're applied correctly.

## Development

### Prerequisites
- Node.js (v14+) or Bun
- Git

### Setup Development Environment
```bash
# Clone repository
git clone https://github.com/wavelog/WaveLogGate.git
cd WaveLogGate

# Install dependencies
npm install
# or with bun
bun install

# Start development mode
npm start
# or with bun
bun start

# Build application
npm run make
```

### Development Notes
- Configuration stored in application data directory
- Debug console available in development mode
- Single instance enforcement (only one can run at a time)

## Network Ports

- **2333/UDP**: WSJT-X and ADIF log reception
- **54321/HTTP**: Frequency/mode control API
- **54322/WebSocket**: Real-time radio status broadcasting
- **12345**: Default FLRig port (if used)
- **4532**: Default Hamlib port (if used)

## Troubleshooting

### Common Issues

#### Port Conflicts
- Ensure ports 2333, 54321, and 54322 are not blocked
- Stop other applications using these ports
- Application shows clear error messages for port conflicts

#### Radio Connection Issues
- Verify FLRig/Hamlib is running and accessible
- Check host/port configuration matches your radio control software
- Test connectivity using the "Test" button in WaveLogGate

#### WaveLog Connection Issues
- Verify WaveLog URL is correct and accessible
- Check API key is valid and not expired
- Ensure Station ID exists in your WaveLog instance
- HTTPS must be enabled on WaveLog

#### Linux Specific Issues
- Some distributions may need additional libraries:
  ```bash
  # For Raspberry Pi or some Linux distributions
  sudo apt-get install libasound2-dev
  ```
  See [DB4SCW's guide](https://www.db4scw.de/getting-waveloggate-to-run-on-the-raspberry-pi/) for detailed Raspberry Pi setup.

#### macOS Apple Silicon Issues
If the app won't start on Apple Silicon Mac:
```bash
xattr -d com.apple.quarantine /Applications/WaveLogGate.app
```

### Debug Information
- Check the application log for detailed error messages
- Use Ctrl+Shift+D to access advanced settings
- In development mode, use the browser console for debugging

## Contributing

Contributions are welcome! Please:

1. Fork the repository
2. Create a feature branch
3. **Submit pull requests to the `dev` branch only**
4. Follow the existing code style
5. Test changes across platforms if possible

### Notable Contributors
- [gotoradio](https://github.com/gotoradio)
- [Northrup](https://github.com/northrup)
- [Frédéric (ON4PFD)](https://github.com/fred-corp)

## Version History

- **v1.1.x**: Current stable version with full WebSocket support and dual profiles
- **v1.0.x**: Basic FLRig and WSJT-X integration
- Earlier versions: Limited feature set

## License

This project is licensed under the terms specified in the [LICENSE](LICENSE) file.

## Support

- **Issues**: Report via [GitHub Issues](https://github.com/wavelog/WaveLogGate/issues)
- **Documentation**: See additional README files in the repository for specific features
- **WaveLog**: [WaveLog Website](https://wavelog.org/) for logging system support
