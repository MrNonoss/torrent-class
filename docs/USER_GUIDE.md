# User Guide: How to use Torrent Class

Torrent Class is a simple way to share large files (like student folders, installers, and so on) with many people on the same WiFi or Ethernet network reliably and efficiently, using the torrent technology.

## 🚀 Quick Start for the "instructors" (The Seeders)

1. **Open your terminal** in the folder where "torrent-class" binary is.
2. **Start Sharing**: 
   ```powershell
   .\torrent-class-windows-amd64.exe -m seed -p D:/Student-folder-2025/
   ```
3. **Look at the screen**: You will see a green URL under **"Deploy"** (e.g., `http://192.168.1.50:8000`).

## 📥 Quick Start for "Students" (The Downloaders)

1. **Download the app**: Open the URL shown on the "seeding" page and download the listed binary for the machine you are using (Windows, Mac, or Linux).
2. **Run it**: Open a terminal where the binary is and run it without any arguments (eg:`.\torrent-class-windows-amd64.exe`, or `./torrent-class-darwin-arm64`).
3. **Wait**: The app will automatically find the seeder and start downloading. You don't need to type anything else!

## 🛠️ Advanced Options

By default, the app downloads to your **current folder**. If you want to save it somewhere else, use the `-p` flag:

```powershell
.\torrent-class-windows-amd64.exe -p C:\Downloads
```

### All Flags:
- `-m seed`: Start sharing a file.
- `-p <path>`: Choose what to share or where to save.
- `-i <ip>`: If the app picks the wrong network IP, you can type yours here (eg: `-i 192.168.1.50`).
- `-s 8000`: Change the web server port (where people download the app).

## ❓ Frequently Asked Questions

**"It says 'WAITING FOR METADATA' for a long time."**
- Make sure you are on the same WiFi as the instructor.
- Check if your Firewall is blocking the app. You may need to "Allow" it when Windows asks.

**"Can I speed it up?"**
- Yes! If you already have some of the files on a USB drive, copy them to your download folder *before* starting the app. It will check the files and only download what is missing.

**"Do I help others?"**
- Yes! As soon as you start downloading, your computer automatically helps other students find the file and shares the parts you already have with them.
