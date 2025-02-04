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
	stopCh          chan struct{}
}

func NewAddressController(clientset *kubernetes.Clientset) (*AddressController, error) {
	factory := informers.NewSharedInformerFactory(clientset, 0)
	serviceInformer := factory.Core().V1().Services().Informer()

	c := &AddressController{
		clientset:       clientset,
		serviceInformer: serviceInformer,
		interfaceIPs:    make(map[string]string),
		stopCh:          make(chan struct{}),
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
	go c.serviceInformer.Run(c.stopCh)

	// start address monitoring
	go c.monitorInterfaces()

	<-c.stopCh
}

func (c *AddressController) Stop() {
	close(c.stopCh)
}

func (c *AddressController) monitorInterfaces() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:

			interfaces, err := net.Interfaces()
			if err != nil {
				log.Printf("Error getting interfaces: %v", err)
				continue
			}

			names := make([]string, 0)
			for _, iface := range interfaces {
				names = append(names, iface.Name)
			}
			log.Printf("checking interfaces: [%s]", strings.Join(names, ","))

			// check for changes in the interfaces
			for _, iface := range interfaces {
				newIP, err := getIP(iface.Name)
				if err != nil {
					log.Printf("Error getting IP for interface [%s]: %v", iface.Name, err)
					continue
				}

				oldIP := c.interfaceIPs[iface.Name]
				if oldIP != newIP {
					log.Printf("IP changed for [%s] from [%s] => [%s]", iface.Name, oldIP, newIP)
					c.updateServicesForInterface(iface.Name, oldIP, newIP)
					c.interfaceIPs[iface.Name] = newIP
				}
			}
		case <-c.stopCh:
			return
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

			// new external IPs
			newExternalIPs := make([]string, 0)

			// keep IPs from other interfaces
			for _, ip := range updatedService.Spec.ExternalIPs {
				if ip != oldIP {
					newExternalIPs = append(newExternalIPs, ip)
				}
			}

			// add the new IP
			newExternalIPs = append(newExternalIPs, newIP)

			// only update on change
			if !stringSlicesEqual(updatedService.Spec.ExternalIPs, newExternalIPs) {
				updatedService.Spec.ExternalIPs = newExternalIPs
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

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	aMap := make(map[string]struct{}, len(a))
	bMap := make(map[string]struct{}, len(b))

	for _, v := range a {
		aMap[v] = struct{}{}
	}
	for _, v := range b {
		bMap[v] = struct{}{}
	}

	for k := range aMap {
		if _, ok := bMap[k]; !ok {
			return false
		}
	}
	return true
}

func (c *AddressController) ensureServiceHasIP(service *corev1.Service) {
	interfaceName := getInterfaceAnnotation(service)
	if interfaceName == "" {
		return
	}

	ip, exists := c.interfaceIPs[interfaceName]
	if !exists {

		newIP, err := getIP(interfaceName)
		if err != nil {
			log.Printf("Error getting IP for interface [%s]: %v", interfaceName, err)
			return
		}
		ip = newIP
		c.interfaceIPs[interfaceName] = ip
	}

	hasIP := false
	for _, existingIP := range service.Spec.ExternalIPs {
		if existingIP == ip {
			hasIP = true
			break
		}
	}

	if !hasIP {
		updatedService := service.DeepCopy()
		updatedService.Spec.ExternalIPs = append(updatedService.Spec.ExternalIPs, ip)

		_, err := c.clientset.CoreV1().Services(service.Namespace).Update(
			context.Background(), updatedService, metav1.UpdateOptions{})
		if err != nil {
			log.Printf("Error updating service %s/%s: %v",
				service.Namespace, service.Name, err)
		}
	}
}

func getInterfaceAnnotation(service *corev1.Service) string {
	return service.Annotations["external-ip-interface"]
}

func (c *AddressController) handleAdd(obj interface{}) {
	service := obj.(*corev1.Service)
	c.ensureServiceHasIP(service)
}

func (c *AddressController) handleUpdate(old, new interface{}) {
	service := new.(*corev1.Service)
	c.ensureServiceHasIP(service)
}
