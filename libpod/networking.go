package libpod

import (
	"net"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/cri-o/ocicni/pkg/ocicni"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// Get an OCICNI network config
func getPodNetwork(id, name, nsPath string, ports []ocicni.PortMapping) ocicni.PodNetwork {
	return ocicni.PodNetwork{
		Name:         name,
		Namespace:    name, // TODO is there something else we should put here? We don't know about Kube namespaces
		ID:           id,
		NetNS:        nsPath,
		PortMappings: ports,
	}
}

// Create and configure a new network namespace for a container
func (r *Runtime) createNetNS(ctr *Container) (err error) {
	ctrNS, err := ns.NewNS()
	if err != nil {
		return errors.Wrapf(err, "error creating network namespace for container %s", ctr.ID())
	}
	defer func() {
		if err != nil {
			if err2 := ctrNS.Close(); err2 != nil {
				logrus.Errorf("Error closing partially created network namespace for container %s: %v", ctr.ID(), err2)
			}
		}
	}()

	logrus.Debugf("Made network namespace at %s for container %s", ctrNS.Path(), ctr.ID())
	podNetwork := getPodNetwork(ctr.ID(), ctr.Name(), ctrNS.Path(), ctr.config.PortMappings)

	_, err = r.netPlugin.SetUpPod(podNetwork)
	if err != nil {
		return errors.Wrapf(err, "error configuring network namespace for container %s", ctr.ID())
	}

	ctr.state.NetNS = ctrNS

	return nil
}

// Join an existing network namespace
func joinNetNS(path string) (ns.NetNS, error) {
	ns, err := ns.GetNS(path)
	if err != nil {
		return nil, errors.Wrapf(err, "error retrieving network namespace at %s", path)
	}

	return ns, nil
}

// Get a container's IP address
func (r *Runtime) getContainerIP(ctr *Container) (net.IP, error) {
	if ctr.state.NetNS == nil {
		return nil, errors.Wrapf(ErrInvalidArg, "container %s has no network namespace, cannot get IP", ctr.ID())
	}

	podNetwork := getPodNetwork(ctr.ID(), ctr.Name(), ctr.state.NetNS.Path(), ctr.config.PortMappings)

	ipStr, err := r.netPlugin.GetPodNetworkStatus(podNetwork)
	if err != nil {
		return nil, errors.Wrapf(err, "error retrieving network status of container %s", ctr.ID())
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		return nil, errors.Wrapf(ErrInternal, "error parsing IP address %s for container %s", ipStr, ctr.ID())
	}

	return ip, nil
}

// Tear down a network namespace
func (r *Runtime) teardownNetNS(ctr *Container) error {
	if ctr.state.NetNS == nil {
		// The container has no network namespace, we're set
		return nil
	}

	logrus.Debugf("Tearing down network namespace at %s for container %s", ctr.state.NetNS.Path(), ctr.ID())

	podNetwork := getPodNetwork(ctr.ID(), ctr.Name(), ctr.state.NetNS.Path(), ctr.config.PortMappings)

	// The network may have already been torn down, so don't fail here, just log
	if err := r.netPlugin.TearDownPod(podNetwork); err != nil {
		logrus.Errorf("Failed to tear down network namespace for container %s: %v", ctr.ID(), err)
	}

	if err := ctr.state.NetNS.Close(); err != nil {
		return errors.Wrapf(err, "error closing network namespace for container %s", ctr.ID())
	}

	ctr.state.NetNS = nil

	return nil
}
