#!/bin/bash
bind="$1"
subnet="$2"
interface="$3"

ip_to_binary() {
    local ip=$1
    local binary=""
    IFS='.' read -r -a octets <<< "$ip"
    for octet in "${octets[@]}"; do
        binary+="$(printf "%08d" $(echo "ibase=10; obase=2; $octet" | bc))"
    done
    echo "$binary"
}

# Function to generate a random number between a given range
random_num() {
    local min=$1
    local max=$2
    echo $((RANDOM % (max - min + 1) + min))
}

# Function to generate a random IP address within a subnet
random_ip() {
    local subnet=$1
    local ip_addr prefix
    IFS='/' read -r ip_addr prefix <<< "$subnet"
    local ip_binary=$(ip_to_binary "$ip_addr")
    local prefix_len=$((32 - $prefix))

    # Mask out the network portion of the IP address
    local network_binary="${ip_binary:0:$prefix}"

    # Generate random host bits within the remaining range
    local host_bits=$((32 - $prefix))
    local random_host=""
    for ((i=1; i<=host_bits; i++)); do
        random_host+=$(random_num 0 1)
    done

    # Combine network address and random host bits to get the random IP
    local random_ip_binary="${network_binary}${random_host}"

    # Convert binary IP address to decimal
    local random_ip=""
    for ((i=0; i<32; i+=8)); do
        random_ip+="$((2#${random_ip_binary:$i:8}))."
    done
    random_ip=${random_ip%?}

    echo "$random_ip"
}

check_ip() {
    local ip=$1
    ping -c 1 -W 1 $ip >/dev/null 2>&1
    return $?
}

# Function to set new IP
rotate() {
    IFS='/' read -r ip_addr prefix <<< "$subnet"
    old_ip=$(ip addr show $interface | awk '/inet / {print $2}' | grep -v $bind)
    IFS='/' read -r oldip_addr oldprefix <<< "$old_ip"
    while true; do
        new_ip=$(random_ip $subnet)
        if check_ip $new_ip; then
            continue
        else
            echo $new_ip
            sudo ip addr add $new_ip/$prefix dev $interface
            sudo ip addr del $oldip_addr/$oldprefix dev $interface
            break
        fi
    done
}

rotate