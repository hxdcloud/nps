#!/bin/bash
server=$1
vkey=$2

red='\033[0;31m'
green='\033[0;32m'
yellow='\033[0;33m'
plain='\033[0m'

cur_dir=$(pwd)

# check root
[[ $EUID -ne 0 ]] && echo -e "${red}错误：${plain} 必须使用root用户运行此脚本！\n" && exit 1

# check os
if [[ -f /etc/redhat-release ]]; then
    release="centos"
elif cat /etc/issue | grep -Eqi "debian"; then
    release="debian"
elif cat /etc/issue | grep -Eqi "ubuntu"; then
    release="ubuntu"
elif cat /etc/issue | grep -Eqi "centos|red hat|redhat"; then
    release="centos"
elif cat /proc/version | grep -Eqi "debian"; then
    release="debian"
elif cat /proc/version | grep -Eqi "ubuntu"; then
    release="ubuntu"
elif cat /proc/version | grep -Eqi "centos|red hat|redhat"; then
    release="centos"
else
    echo -e "${red}未检测到系统版本，请联系脚本作者！${plain}\n" && exit 1
fi

arch=$(uname -m)

if [[ $arch == "x86_64" || $arch == "x64" || $arch == "amd64" ]]; then
  arch="amd64"
elif [[ $arch == "i386" || $arch == "x86" ]]; then
  arch="386"
elif [[ $arch == "aarch64" || $arch == "arm64" ]]; then
  arch="arm64"
elif [[ $arch == "armv7" || $arch == "arm_v7" || $arch == "armv7l" || $arch == "armv71" ]]; then
  arch="arm_v7"
elif [[ $arch == "armv6" || $arch == "arm_v6" || $arch == "armv6l" || $arch == "armv61" ]]; then
  arch="arm_v6"
elif [[ $arch == "armv5" || $arch == "arm_v5" || $arch == "armv5l" || $arch == "armv51" ]]; then
  arch="arm_v5"
elif [[ $arch == "mips64" ]]; then
  arch="mips64"
elif [[ $arch == "mips64le" ]]; then
  arch="mips64le"
elif [[ $arch == "mips" ]]; then
  arch="mips"
elif [[ $arch == "mipsle" ]]; then
  arch="mipsle"
else
  arch="amd64"
  echo -e "${red}检测架构失败，使用默认架构: ${arch}${plain}"
fi

echo "架构: ${arch}"

os_version=""

# os version
if [[ -f /etc/os-release ]]; then
    os_version=$(awk -F'[= ."]' '/VERSION_ID/{print $3}' /etc/os-release)
fi
if [[ -z "$os_version" && -f /etc/lsb-release ]]; then
    os_version=$(awk -F'[= ."]+' '/DISTRIB_RELEASE/{print $2}' /etc/lsb-release)
fi

if [[ x"${release}" == x"centos" ]]; then
    if [[ ${os_version} -le 6 ]]; then
        echo -e "${red}请使用 CentOS 7 或更高版本的系统！${plain}\n" && exit 1
    fi
elif [[ x"${release}" == x"ubuntu" ]]; then
    if [[ ${os_version} -lt 16 ]]; then
        echo -e "${red}请使用 Ubuntu 16 或更高版本的系统！${plain}\n" && exit 1
    fi
elif [[ x"${release}" == x"debian" ]]; then
    if [[ ${os_version} -lt 8 ]]; then
        echo -e "${red}请使用 Debian 8 或更高版本的系统！${plain}\n" && exit 1
    fi
fi

install_base() {
    if [[ x"${release}" == x"centos" ]]; then
        yum install epel-release -y
        yum install wget curl tar -y
    else
        apt install wget curl tar -y
    fi
}

# 0: running, 1: not running, 2: not installed
check_status() {
    if [[ ! -f /etc/systemd/system/Npc.service ]]; then
        return 2
    fi
    temp=$(systemctl status Npc | grep Active | awk '{print $3}' | cut -d "(" -f2 | cut -d ")" -f1)
    if [[ x"${temp}" == x"running" ]]; then
	echo -e "${green} 客户端正在运行"
        return 0
    else
	echo -e "${red} 客户端没有运行"
        return 1
    fi
}

install_npc() {
    cd /root/
    if [[ -e /root/hxdcloud/npc/ ]]; then
        rm /root/hxdcloud/npc/ -rf
    fi
    mkdir -p /root/hxdcloud/npc && cd /root/hxdcloud/npc
    wget -N --no-check-certificate -O /root/hxdcloud/npc/npc.tar.gz https://download.fastgit.org/hxdcloud/nps/releases/download/v0.26.10/linux_${arch}_client.tar.gz
    if [[ ! -e /root/hxdcloud/npc/npc.tar.gz ]]; then
        echo "下载失败，请联系脚本作者！"
	exit 1
    fi
    tar zxvf npc.tar.gz
    rm npc.tar.gz -f
    /root/hxdcloud/npc/npc uninstall
    /root/hxdcloud/npc/npc install "-server=$server" "-vkey=$vkey"
    echo -e "${green} 客户端安装完成，已设置开机自启"
    npc start
    check_status

    echo -e ""
    echo "npc 管理脚本使用方法: "
    echo "------------------------------------------"
    echo "npc start              - 启动 npc"
    echo "npc stop               - 停止 npc"
    echo "npc restart            - 重启 npc"
    echo "------------------------------------------"
}

echo -e "${green}开始安装${plain}"
install_base
install_npc