# container-lab

## Usage

## Go

- build

```bash
go build -o gobox go_box.go
```

- run

```bash
sudo ./gobox run
```

- child

```bash
sudo ./gobox child
```

## Test Tool

- Install Essential Tools

```bash
apt update
apt-get install -y iproute2 iputils-ping net-tools curl htop
apt-get install -y stress-ng
```

- Test ram with Stress-ng

```bash
stress-ng --vm 1 --vm-bytes 600M --vm-keep --timeout 30s
```

- Install Python

```bash
apt update
apt-get install -y python3 python3-pip python3-venv
```

- python test ram

```bash
python3 -c 'import time; x=bytearray(600*1024*1024); [x.__setitem__(i,1) for i in range(0,len(x),4096)]; print("Using 600 MB"); time.sleep(300)'
```

```bash
python3 -c 'import time,os; x=bytearray(600*1024*1024); [x.__setitem__(i,1) for i in range(0,len(x),4096)]; print(f"PID={os.getpid()} allocated 600 MB",flush=True); time.sleep(300)'
```
