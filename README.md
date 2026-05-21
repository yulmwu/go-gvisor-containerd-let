<p align="center">
  <img src="./assets/banner.png" alt="sandboxd-o">
</p>


# sandboxd-o — Containerd CRI and gVisor shim based sandbox runtime, orchestrator

> [!WARNING]
> 
> This project provides a sandbox environment, but **fundamentally operates on top of container technology**. (Containers are not a [sandbox](https://en.wikipedia.org/wiki/Sandbox_(computer_security)))
> 
> Even though gVisor offers stronger isolation, there are still limitations compared to more robust isolation mechanisms such as Firecracker Micro-VMs or KVM-based virtualization.
> 
> Therefore, this project should be used with those limitations in mind. It is recommended to deploy it in a dedicated computing environment (worker nodes) rather than running it alongside environments containing critical production data.
> 
> While the likelihood of container escape may be low, it is important to remember that container technology does not fundamentally provide a perfectly isolated sandbox environment.


![full architecture](./assets/full.drawio.png)

# sbxlet Architecture

![sbxlet Architecture](./assets/sbxlet-architecture.drawio.png)

# sbxlet Networking Model

![sbxlet Networking Model](./assets/sbxlet-networking-model.drawio.png)
