package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type AddressController struct {
	clientset       *kubernetes.Clientset
	serviceInformer cache.SharedIndexInformer
	interfaceIPs    map[string]string
	channelStop     chan struct{}
	channelTrigger  chan struct{}
}

func NewAddressController(clientset *kubernetes.Clientset) (*AddressController, error) {
	factory := informers.NewSharedInformerFactory(clientset, 0)
	serviceInformer := factory.Core().V1().Services().Informer()

	c := &AddressController{
		clientset:       clientset,
		serviceInformer: serviceInformer,
		interfaceIPs:    make(map[string]string),
		channelStop:     make(chan struct{}),
		channelTrigger:  make(chan struct{}, 1),
	}

	serviceInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    c.handleAdd,
		UpdateFunc: c.handleUpdate,
	})

	return c, nil
}

func getIP(interfaceName string) (string, error) {

	ifaces, err := net.Interfaces()
	if err != nil {
		return "", fmt.Errorf("failed to get interfaces: %v", err)
	}

	for _, iface := range ifaces {
		if iface.Name != interfaceName {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				if ipnet.IP.To4() != nil {
					return ipnet.IP.String(), nil
				}
			}
		}
	}

	names := make([]string, 0)
	for _, iface := range ifaces {
		names = append(names, iface.Name)
	}

	return "", fmt.Errorf("no address found for interface [%s] in [%v]", interfaceName, strings.Join(names, ","))
}

func (c *AddressController) Run() {

	// start the service informer
	go c.serviceInformer.Run(c.channelStop)

	// start address monitoring
	go c.monitorInterfaces()

	<-c.channelStop
}

func (c *AddressController) Stop() {
	close(c.channelStop)
}

func (c *AddressController) monitorInterfaces() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.checkInterfacesAndUpdateServices()
		case <-c.channelTrigger:
			c.checkInterfacesAndUpdateServices()
		case <-c.channelStop:
			return
		}
	}
}

func (c *AddressController) checkInterfacesAndUpdateServices() {
	interfaces, err := net.Interfaces()
	if err != nil {
		log.Printf("Error getting interfaces: %v", err)
		return
	}

	// log.Printf("addresses: %v", c.interfaceIPs)

	// check for changes in the interfaces
	for _, iface := range interfaces {
		newIP, err := getIP(iface.Name)
		if err != nil {
			continue
		}

		oldIP := c.interfaceIPs[iface.Name]
		if oldIP != newIP {
			log.Printf("IP changed for [%s] from [%s] => [%s]", iface.Name, oldIP, newIP)
			c.updateServicesForInterface(iface.Name, oldIP, newIP)
			c.interfaceIPs[iface.Name] = newIP
		}
	}
}

func (c *AddressController) updateServicesForInterface(interfaceName, oldIP, newIP string) {
	services, err := c.clientset.CoreV1().Services("").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		log.Printf("Error listing services: %v", err)
		return
	}

	for _, service := range services.Items {
		if interfaceName == getInterfaceAnnotation(&service) {
			updatedService := service.DeepCopy()

			// set of new IPs
			newIPs := make(map[string]struct{})
			for _, ip := range updatedService.Spec.ExternalIPs {
				if ip != oldIP {
					newIPs[ip] = struct{}{}
				}
			}
			newIPs[newIP] = struct{}{}

			// set of old IPs
			oldIPs := make(map[string]struct{})
			for _, ip := range updatedService.Spec.ExternalIPs {
				oldIPs[ip] = struct{}{}
			}

			if !setsAreEqual(oldIPs, newIPs) {

				// convert set to array
				newExternalIPs := make([]string, 0, len(newIPs))
				for ip := range newIPs {
					newExternalIPs = append(newExternalIPs, ip)
				}

				updatedService.Spec.ExternalIPs = newExternalIPs

				// update the service
				_, err := c.clientset.CoreV1().Services(service.Namespace).Update(
					context.Background(), updatedService, metav1.UpdateOptions{})
				if err != nil {
					log.Printf("Error updating service %s/%s: %v",
						service.Namespace, service.Name, err)
				} else {
					log.Printf("Updated externalIP for service %s/%s",
						service.Namespace, service.Name)
				}
			}
		}
	}
}

func setsAreEqual(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}

	for ip := range a {
		if _, exists := b[ip]; !exists {
			return false
		}
	}
	for ip := range b {
		if _, exists := a[ip]; !exists {
			return false
		}
	}

	return true
}

func getInterfaceAnnotation(service *corev1.Service) string {
	return service.Annotations["external-ip-interface"]
}

func (c *AddressController) handleAdd(obj interface{}) {
	log.Printf("re-evaluation triggered by add")
	// try but don't block
	select {
	case c.channelTrigger <- struct{}{}:
	default:
	}
}

func (c *AddressController) handleUpdate(old, new interface{}) {
	log.Printf("re-evaluation triggered by update")
	// try but don't block
	select {
	case c.channelTrigger <- struct{}{}:
	default:
	}
}
