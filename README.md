# CAT and WSJT-X Bridge for Wavelog

## Prerequisites

* [FLRig](http://www.w1hkj.com/) properly SetUp to your TRX (if you want to use the CAT-Feature. It's optional!)
* WSJT-X (if you want to use the automatic logging from WSJT-X/Z/Y / FLDigi / Tool which produces UDP-Log-Packets)
* WaveLog-Account on any WaveLog instance
* HTTPS (SSL) has to be enabled for Wavelog to use WLGate

## WARNING // IMPORTANT! (When using AppleSilicon Mac)

If you use a newer mac (with M1,M2,M3, etc.) apple changed their policy for unsigned Apps.
There's a workaround available, but you need the Terminal aka Shell for that.
Instructions:

1. Download Binary/DMG
2. Copy Binary/DMG to Application-Folder
3. Launch Terminal.app
4. Type in the following:
   * `xattr -d com.apple.quarantine /Applications/WaveLogGate.app`
   * Launch the Application (should launch now)

## Setup

1. Download Binary
2. Start Binary (for Windows: Start Binary and after that you have a NEW Binary. One can delete the old one)
3. Fill in informations:
   * WAVELog-URL including index.php (if you setted it up with that)
   * API-Key (from Wavelog / Right Menu / API-Keys)
   * Station-ID (from Wavelog / Right Menu / See Stationlocations / small badge with station-ID)
4. If you're running FLRig on the same machine put 127.0.0.1 to FLRig-Host and 12345 to FLRig Port and enable it.
5. Click "Test" - Button becomes green if working, Red with detailled issue below, when faulty.
6. Click "Save" if everything is okay

## WSJT-X (and derivates) SetUp

Go To WSJT-X-Settings // Reporting
Enable "Secondary UDP Server" like shown in the picture. Do NOT set "UDP Server" (above) to the same Port!

![image](https://github.com/wavelog/waveloggate/assets/1410708/7238b193-c589-4ae3-97f8-eae506965dff)

## SetUp/Working with CAT in Wavelog

1. Open Live-Logging
2. Set Radio at Radio-Tab
3. Do QSO
4. Log

Radio will be saved for next QSO

## Features

* When clicking on a spot in WaveLog-Bandmap your TRX with QSY to the Spot.
* If you log a (non WSJT-X) QSO first go to "Stations Tab" and chose "WLGate" as Radio. After that Band/Mode/QRG will be automatically taken from your Rig into the QSO-Fields
* When clicking the loupe at Live-QSO/Post-QSO Wavelog will automaticly lookup the Spot behind the QRG (if there's a spot)

Enjoy

## Advanced Settings (Beta!)
* Press CTRL+SHIFT+D in config-Window. A second window appears.
* You can enable Hamlib instead of FLRig here.
* You can disable taking over the Power to Wavelog here.
* WARNING: This is in Beta-State. The main-config-window may(!) not show all changes. To make sure whats saved, pse restart the Application after changing the advanced-config

## Contributing

Contribution is welcome. PRs will only be accepted against the Dev-Branch.

## Contributions
Tnx for contributing:
[gotoradio](https://github.com/gotoradio) [Northrup](https://github.com/northrup) [Frédéric (ON4PFD)](https://github.com/fred-corp)

## Troubleshooting
Since we are not able to test every Linux-Version out there, we're collecting some "workarounds" and tips if there are problems with the Linux-Version.
* Problem: libasound.so.2 cannot be found AND possible Problems for Raspberry: [Blogpost from DB4SCW (Thank u!)](https://www.db4scw.de/getting-waveloggate-to-run-on-the-raspberry-pi/)
##
